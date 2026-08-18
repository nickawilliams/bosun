package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// resetTimeline puts the package's timeline state back to "nothing has
// printed yet" and restores it afterwards, so a test that prints cards
// can't leak a spacer flag or an open-card record into its neighbours.
func resetTimeline(t *testing.T) {
	t.Helper()
	prevSpacer, prevOpen := needsSpacer, openCard
	needsSpacer, openCard = false, nil
	t.Cleanup(func() { needsSpacer, openCard = prevSpacer, prevOpen })
}

// captureStdout runs fn with os.Stdout redirected to a pipe and
// returns everything written. The card printers write through fmt's
// package-level helpers, so the pipe is the only seam.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()
	defer func() {
		os.Stdout = orig
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	return <-read
}

// rewriteEscape is the cursor-up + clear-to-end sequence the timeline
// uses to swap a card from open to continuing form.
func rewriteEscape(lines int) string {
	return fmt.Sprintf("\x1b[%dF\x1b[J", lines)
}

// gutters returns the leading gutter (everything left of the content
// margin) of each rendered line, ANSI stripped and measured in cells
// rather than bytes — the glyphs are all multi-byte. That gutter is
// the whole visual contract of the open/continuing swap: it changes,
// the content column does not.
func gutters(rendered string) []string {
	var out []string
	for line := range strings.SplitSeq(strings.TrimRight(rendered, "\n"), "\n") {
		cells := []rune(ansi.Strip(line))
		if len(cells) > ContentCol(0)-1 {
			cells = cells[:ContentCol(0)-1]
		}
		out = append(out, string(cells))
	}
	return out
}

// titleColumn returns the 1-indexed column where title starts in an
// ANSI-stripped line, counting cells rather than bytes.
func titleColumn(line, title string) int {
	before, _, found := strings.Cut(line, title)
	if !found {
		return -1
	}
	return len([]rune(before)) + 1
}

// TestCardFormsDrawTheSpecifiedGutters locks the two forms against the
// visual spec in issue #27: an open card's body lines are blank in the
// gutter (the timeline visibly ends), a continuing card's carry the
// spine, and the content margin is identical either way.
func TestCardFormsDrawTheSpecifiedGutters(t *testing.T) {
	card := NewCard(CardSuccess, "card one").Muted("body line", "body line", "body line")

	wantOpen := []string{" ✓  ", "    ", "    ", "    "}
	if got := gutters(card.Render()); !equalStrings(got, wantOpen) {
		t.Errorf("open gutters = %q, want %q", got, wantOpen)
	}

	wantCont := []string{" ✓  ", " │  ", " │  ", " │  "}
	if got := gutters(card.renderContinuing()); !equalStrings(got, wantCont) {
		t.Errorf("continuing gutters = %q, want %q", got, wantCont)
	}
}

// TestGlyphlessCardAbsorbsIntoTimeline covers the special case: a card
// whose state carries no glyph borrows the spine as its own marker —
// ├─ with a successor below, ╰─ as the tail — and content still lands
// at ContentCol(0) because the two-cell marker eats one gap column.
func TestGlyphlessCardAbsorbsIntoTimeline(t *testing.T) {
	// cardStateCount is past the end of the glyph vocabulary, so
	// stateGlyph reports no glyph for it — the only way to build a
	// glyphless card.
	card := NewCard(cardStateCount, "card three").Muted("body line")

	openLines := strings.Split(strings.TrimRight(ansi.Strip(card.Render()), "\n"), "\n")
	contLines := strings.Split(strings.TrimRight(ansi.Strip(card.renderContinuing()), "\n"), "\n")

	if want := " ╰─ Card Three"; openLines[0] != want {
		t.Errorf("open glyph row = %q, want %q", openLines[0], want)
	}
	if want := " ├─ Card Three"; contLines[0] != want {
		t.Errorf("continuing glyph row = %q, want %q", contLines[0], want)
	}
	if want := "    body line"; openLines[1] != want {
		t.Errorf("open body row = %q, want %q", openLines[1], want)
	}
	if want := " │  body line"; contLines[1] != want {
		t.Errorf("continuing body row = %q, want %q", contLines[1], want)
	}
	// Both markers must put the title at the shared content column so
	// a glyphless card lines up with its glyph-bearing neighbours.
	for _, line := range []string{openLines[0], contLines[0]} {
		if col := titleColumn(line, "Card Three"); col != ContentCol(0) {
			t.Errorf("title at column %d, want %d: %q", col, ContentCol(0), line)
		}
	}
}

// TestRenderWithGlyphStaysOpen: the glyph the spinner injects is used
// verbatim with the standard gap, and the frame is open form — an
// animating card is always the timeline's tail. GlyphSlot rows track
// the same glyph, so a gather card's in-flight row animates with it.
func TestRenderWithGlyphStaysOpen(t *testing.T) {
	card := NewCard(CardRunning, "gathering").Item(GlyphSlot, "in flight")
	out := ansi.Strip(card.renderWithGlyph("⣾"))

	if want := " ⣾  Gathering"; !strings.HasPrefix(out, want) {
		t.Errorf("glyph row = %q, want prefix %q", out, want)
	}
	if strings.Contains(out, GlyphSlot) {
		t.Errorf("GlyphSlot was left unresolved: %q", out)
	}
	if strings.Count(out, "⣾") != 2 {
		t.Errorf("GlyphSlot row should track the injected glyph: %q", out)
	}
	if strings.Contains(out, "│") {
		t.Errorf("a spinner frame should render open: %q", out)
	}
}

// TestGlyphlessCardHonorsGlyphColor: a glyphless card borrows the
// spine as its marker in the recessed timeline gray, but an explicit
// GlyphColor still wins — same override contract as a state glyph.
func TestGlyphlessCardHonorsGlyphColor(t *testing.T) {
	plain := NewCard(cardStateCount, "card")
	tinted := NewCard(cardStateCount, "card").GlyphColor(Palette.Success)

	if plain.Render() == tinted.Render() {
		t.Error("GlyphColor should recolor a glyphless card's marker")
	}
	if ansi.Strip(plain.Render()) != ansi.Strip(tinted.Render()) {
		t.Error("GlyphColor should change only the color, not the shape")
	}
}

// TestPrintRewritesPredecessorIntoContinuingForm is the end-to-end
// mechanic: three cards printed in sequence leave the terminal showing
// the issue's Step 3 — the first two continuing, the last open — with
// each rewrite emitted as a cursor-up over exactly the predecessor's
// height.
func TestPrintRewritesPredecessorIntoContinuingForm(t *testing.T) {
	resetTimeline(t)

	one := NewCard(CardSuccess, "card one").Muted("body line", "body line")
	two := NewCard(CardSuccess, "card two").Muted("body line", "body line")
	three := NewCard(CardSuccess, "card three").Muted("body line", "body line")

	out := captureStdout(t, func() {
		one.Print()
		two.Print()
		three.Print()
	})

	// Each card is 3 lines tall, so each rewrite moves up 3.
	if n := strings.Count(out, rewriteEscape(3)); n != 2 {
		t.Errorf("got %d rewrites of height 3, want 2:\n%q", n, out)
	}
	// The rewritten copies are the continuing form; the tail is open.
	if !strings.Contains(out, one.renderContinuing()) {
		t.Errorf("card one was never rewritten into continuing form:\n%q", out)
	}
	if !strings.Contains(out, two.renderContinuing()) {
		t.Errorf("card two was never rewritten into continuing form:\n%q", out)
	}
	if !strings.HasSuffix(out, three.Render()) {
		t.Errorf("timeline should end with card three in open form:\n%q", out)
	}
	if strings.Contains(out, three.renderContinuing()) {
		t.Errorf("the last card must stay open:\n%q", out)
	}

	// What the terminal ends up showing: replay the escapes.
	want := strings.Join([]string{
		trimTrailingNewline(one.renderContinuing()),
		" " + (&Card{}).renderConnector(),
		trimTrailingNewline(two.renderContinuing()),
		" " + (&Card{}).renderConnector(),
		trimTrailingNewline(three.Render()),
	}, "\n")
	if got := replayRewrites(out); got != want {
		t.Errorf("final screen mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestEndTimelineLeavesLastCardOpen locks the termination rule:
// EndTimeline never rewrites, so the closing card keeps its spineless
// body — that absence is the "this is the end" signal — and the record
// is dropped so a later card can't reach back across the blank line.
func TestEndTimelineLeavesLastCardOpen(t *testing.T) {
	resetTimeline(t)

	card := NewCard(CardSuccess, "last").Muted("body line")
	out := captureStdout(t, func() {
		card.Print()
		EndTimeline()
	})

	if strings.Contains(out, "\x1b[") && strings.Contains(out, "\x1b[J") {
		t.Errorf("EndTimeline rewrote the last card:\n%q", out)
	}
	if openCard != nil {
		t.Error("EndTimeline left an open-card record behind")
	}
	if want := card.Render() + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestFinalizeOpenCardRestoresSpineBeforeHandoff covers the escape
// hatch for output that paints below a card without going through the
// spacer prefix (huh forms, raw blocks): the spine is restored up
// front, so the card reads as continuing while the borrowed region is
// live, and nothing is left to rewrite afterwards.
func TestFinalizeOpenCardRestoresSpineBeforeHandoff(t *testing.T) {
	resetTimeline(t)

	card := NewCard(CardSuccess, "header").Muted("body line")
	out := captureStdout(t, func() {
		card.Print()
		FinalizeOpenCard()
	})

	if !strings.HasSuffix(out, rewriteEscape(2)+card.renderContinuing()) {
		t.Errorf("FinalizeOpenCard did not rewrite the card in place:\n%q", out)
	}
	if openCard != nil {
		t.Error("FinalizeOpenCard left an open-card record behind")
	}

	// Idempotent: a second call has nothing to do.
	if again := captureStdout(t, FinalizeOpenCard); again != "" {
		t.Errorf("second FinalizeOpenCard wrote %q, want nothing", again)
	}
}

// TestRewindDiscardsOpenCardRecord guards the cursor math around
// transient cards: once a rewind has erased the block, rewriting it
// would move the cursor back over whatever now occupies those rows.
func TestRewindDiscardsOpenCardRecord(t *testing.T) {
	resetTimeline(t)

	captureStdout(t, func() {
		rewind := NewCard(CardInput, "pick one").Muted("body line").PrintRewindable()
		if openCard == nil {
			t.Error("a rewindable card should still record while it is on screen")
		}
		rewind()
	})

	if openCard != nil {
		t.Error("rewind left a record pointing at erased rows")
	}
}

// TestNestedCardsStayContinuing locks the v1 scope boundary: an
// indented card's body sits inside its parent's spine, so it keeps its
// connector regardless of form — dropping it would punch a hole in the
// middle of a group rather than terminate the timeline.
func TestNestedCardsStayContinuing(t *testing.T) {
	child := NewCard(CardSuccess, "child").Indent(1).Muted("body line")
	if child.Render() != child.renderContinuing() {
		t.Errorf("nested card changed with the form:\nopen: %q\ncont: %q",
			child.Render(), child.renderContinuing())
	}
	if !strings.Contains(ansi.Strip(child.Render()), " │   │  body line") {
		t.Errorf("nested card lost its own connector: %q", ansi.Strip(child.Render()))
	}
}

// TestRawModeSkipsRewrites keeps ANSI cursor motion out of piped
// output: raw mode is for streams where escapes would corrupt the
// payload, so nothing records and nothing rewrites.
func TestRawModeSkipsRewrites(t *testing.T) {
	resetTimeline(t)
	prev := defaultReporter
	SetDefault(NewCaptureReporter())
	t.Cleanup(func() { SetDefault(prev) })

	out := captureStdout(t, func() {
		NewCard(CardSuccess, "one").Muted("body").Print()
		NewPlanCard(NewPlan().Add(PlanCreate, "branch", "repo", "api", "x")).Print()
		recordCard(NewCard(CardSuccess, "two").Muted("body"))
		FinalizeOpenCard()
	})

	if out != "" {
		t.Errorf("raw mode wrote %q, want nothing", out)
	}
	if openCard != nil {
		t.Error("raw mode recorded an open card")
	}
}

// TestRecordOpenCardIgnoresEmptyBlocks covers the zero-height case
// (an empty Plan renders to nothing): there is no block to move the
// cursor over, and a stale record would move it over the card above.
func TestRecordOpenCardIgnoresEmptyBlocks(t *testing.T) {
	resetTimeline(t)
	recordCard(NewCard(CardSuccess, "real").Muted("body"))
	recordOpenCard("", "")
	if openCard != nil {
		t.Error("a zero-line block should clear the record, not replace it")
	}
}

// TestRewriteFitsGuardsTheViewport covers the cursor-math limit that
// makes tall cards unrewritable: cursor-previous-line clamps at row 1
// of the screen and cannot reach into scrollback, so a block at least
// as tall as the viewport would be re-entered from the middle and
// duplicated below the stranded rows.
func TestRewriteFitsGuardsTheViewport(t *testing.T) {
	cases := []struct {
		block, viewport int
		want            bool
	}{
		{block: 3, viewport: 24, want: true},   // ordinary card
		{block: 23, viewport: 24, want: true},  // exactly fits above the cursor
		{block: 24, viewport: 24, want: false}, // top row has scrolled off
		{block: 40, viewport: 24, want: false}, // captured subprocess output
		{block: 0, viewport: 24, want: false},  // nothing painted
	}
	for _, tc := range cases {
		if got := rewriteFits(tc.block, tc.viewport); got != tc.want {
			t.Errorf("rewriteFits(%d, %d) = %v, want %v",
				tc.block, tc.viewport, got, tc.want)
		}
	}
}

// TestTallCardIsLeftOpenRatherThanDuplicated is the guard end to end.
// Redirecting the output stream to a non-File makes TermHeight report
// its 24-row default, so the card below is deliberately taller than
// the viewport. Before the guard this emitted a cursor-up the terminal
// could not honour, stranding the top of the card and printing the
// rest of it a second time.
func TestTallCardIsLeftOpenRatherThanDuplicated(t *testing.T) {
	resetTimeline(t)
	SetStreams(nil, &bytes.Buffer{}, nil)
	t.Cleanup(ResetStreams)

	body := make([]string, TermHeight()+5)
	for i := range body {
		body[i] = fmt.Sprintf("body line %d", i)
	}
	tall := NewCard(CardSuccess, "captured output").Muted(body...)

	out := captureStdout(t, func() {
		tall.Print()
		NewCard(CardSuccess, "after").Print()
	})

	if rewritePattern.MatchString(out) {
		t.Errorf("a card taller than the viewport must not be rewritten:\n%q", out)
	}
	if strings.Count(out, "body line 0") != 1 {
		t.Errorf("card body was duplicated:\n%q", out)
	}

	// A card that does fit still swaps, so the guard is not simply
	// disabling the feature whenever streams are redirected.
	resetTimeline(t)
	short := NewCard(CardSuccess, "short").Muted("body line")
	out = captureStdout(t, func() {
		short.Print()
		NewCard(CardSuccess, "after").Print()
	})
	if !rewritePattern.MatchString(out) {
		t.Errorf("a card that fits should still be rewritten:\n%q", out)
	}
}

// TestRecordSnapshotsBothFormsAtPrintTime: the record holds rendered
// bytes, not a pointer back into a live card. PlanCard walks
// Applying → Success in place between its two Prints, and re-deriving
// the continuing form at rewrite time would paint the FINAL state into
// the Applying card's rows — then print the final card again below it.
func TestRecordSnapshotsBothFormsAtPrintTime(t *testing.T) {
	resetTimeline(t)

	pc := NewPlanCard(NewPlan().Add(PlanCreate, "branch", "repo", "api", "feature/x"))
	pc.SetState(PlanApplying)
	applying := pc.renderContinuing()

	out := captureStdout(t, func() {
		pc.Print()
		// The card mutates while it is the recorded open card — the
		// non-interactive RunApply fallback does exactly this.
		pc.SetResults(1, 0, 0)
		pc.SetState(PlanSuccess)
		pc.Print()
	})

	if !strings.Contains(out, applying) {
		t.Errorf("rewrite should have repainted the Applying state it recorded:\n%q", out)
	}
	if n := strings.Count(ansi.Strip(out), "Success"); n != 1 {
		t.Errorf("final state rendered %d times, want 1:\n%q", n, ansi.Strip(out))
	}
}

// TestRecordOpenCardAdoptsAnExternallyPaintedCard: RunCardStepsInto
// paints a card as a raw final frame and hands the region over. When
// the hand-off doesn't happen the caller owns a card the timeline has
// never seen, and RecordOpenCard is how it says so — without it those
// rows keep a blank gutter while the spine resumes below them.
func TestRecordOpenCardAdoptsAnExternallyPaintedCard(t *testing.T) {
	resetTimeline(t)

	painted := NewCard(CardSuccess, "pull requests").Muted("api · ready", "web · ready")
	out := captureStdout(t, func() {
		fmt.Print(painted.Render()) // stands in for the raw final frame
		RecordOpenCard(painted)
		NewCard(CardSuccess, "base branch").Print()
	})

	if !strings.Contains(out, painted.renderContinuing()) {
		t.Errorf("an adopted card should be rewritten once a successor prints:\n%q", out)
	}
}

// TestBoldFinalizesTheOpenCard: Bold is not a card and not on the
// timeline grid, so without the finalize the next card's rewrite would
// reach back over its line and erase it.
func TestBoldFinalizesTheOpenCard(t *testing.T) {
	resetTimeline(t)

	card := NewCard(CardSuccess, "header").Muted("body line")
	out := captureStdout(t, func() {
		card.Print()
		Bold("standalone")
	})

	if !strings.Contains(out, card.renderContinuing()) {
		t.Errorf("Bold should restore the spine before printing:\n%q", out)
	}
	if !strings.HasSuffix(ansi.Strip(out), "standalone\n") {
		t.Errorf("Bold's line should survive as the last output:\n%q", ansi.Strip(out))
	}
	if openCard != nil {
		t.Error("Bold left an open-card record behind")
	}
}

// TestRewindOnlyDiscardsItsOwnRecord: if something printed between a
// rewindable block and its rewind, the record now belongs to that
// successor. Clearing it blindly would leave the successor spineless
// forever.
func TestRewindOnlyDiscardsItsOwnRecord(t *testing.T) {
	resetTimeline(t)

	captureStdout(t, func() {
		rewind := NewCard(CardInput, "transient").Muted("body line").PrintRewindable()
		successor := NewCard(CardSuccess, "successor").Muted("body line")
		successor.Print()
		rewind()

		if openCard == nil {
			t.Fatal("rewind discarded the successor's record")
		}
		if openCard.continuing != successor.renderContinuing() {
			t.Error("the open card should still be the successor")
		}
	})
}

// TestUnchangedFormsSkipTheRewrite: a card with no body renders the
// same either way, and so does the root card's logo box. Repainting
// them would cost a visible flicker for no visible change, so the
// swap is skipped entirely.
func TestUnchangedFormsSkipTheRewrite(t *testing.T) {
	resetTimeline(t)

	out := captureStdout(t, func() {
		NewCard(CardSuccess, "one").Print()
		NewCard(CardSuccess, "two").Print()
	})

	if rewritePattern.MatchString(out) {
		t.Errorf("body-less cards should not be repainted:\n%q", out)
	}
}

// TestPlanFormsDrawTheSpecifiedGutters: a Plan renders through a Card
// internally, so it inherits the swap — its rows lose the spine while
// it is the timeline's tail and get it back once something follows.
func TestPlanFormsDrawTheSpecifiedGutters(t *testing.T) {
	plan := NewPlan().
		Add(PlanCreate, "branch", "repo", "api", "feature/x").
		Add(PlanModify, "status", "issue", "EX-1", "Open → Done")

	wantOpen := []string{" ●  ", "    ", "    "}
	if got := gutters(plan.Render()); !equalStrings(got, wantOpen) {
		t.Errorf("open gutters = %q, want %q", got, wantOpen)
	}
	wantCont := []string{" ●  ", " │  ", " │  "}
	if got := gutters(plan.renderContinuing()); !equalStrings(got, wantCont) {
		t.Errorf("continuing gutters = %q, want %q", got, wantCont)
	}

	resetTimeline(t)
	captureStdout(t, func() {
		rewind := plan.PrintRewindable()
		if openCard == nil {
			t.Error("a rewindable plan should record while on screen")
		}
		rewind()
	})
	if openCard != nil {
		t.Error("plan rewind left a stale record")
	}

	// An empty plan renders nothing in either form, and so records
	// nothing to rewrite.
	empty := NewPlan()
	if empty.Render() != "" || empty.renderContinuing() != "" {
		t.Error("an empty plan should render nothing in both forms")
	}
	if out := captureStdout(t, empty.Print); out != "" {
		t.Errorf("empty plan printed %q, want nothing", out)
	}
	if openCard != nil {
		t.Error("an empty plan should not become the open card")
	}
}

// TestPlanCardPrintParticipatesInTheSwap: PlanCard prints through its
// own path rather than Card.Print, so its record has to be wired up
// separately — this is the check that it was.
func TestPlanCardPrintParticipatesInTheSwap(t *testing.T) {
	resetTimeline(t)

	pc := NewPlanCard(NewPlan().Add(PlanCreate, "branch", "repo", "api", "feature/x"))

	wantOpen := []string{" ?  ", "    "}
	if got := gutters(pc.Render()); !equalStrings(got, wantOpen) {
		t.Errorf("open gutters = %q, want %q", got, wantOpen)
	}
	wantCont := []string{" ?  ", " │  "}
	if got := gutters(pc.renderContinuing()); !equalStrings(got, wantCont) {
		t.Errorf("continuing gutters = %q, want %q", got, wantCont)
	}

	out := captureStdout(t, func() {
		pc.Print()
		NewCard(CardSuccess, "after").Print()
	})
	if !strings.Contains(out, pc.renderContinuing()) {
		t.Errorf("plan card was never rewritten into continuing form:\n%q", out)
	}

	// The rewindable variant erases its own block, so it must leave no
	// record pointing at those rows.
	captureStdout(t, func() {
		rewind := pc.PrintRewindable()
		if openCard == nil {
			t.Error("a rewindable plan card should record while on screen")
		}
		rewind()
	})
	if openCard != nil {
		t.Error("plan card rewind left a stale record")
	}
}

// TestFinalFrameCardMirrorsTheLastView pins the spinner runners'
// record to the card the program actually painted: the successor on
// success, the (now failed) original otherwise. Getting this wrong
// rewrites the wrong number of rows.
func TestFinalFrameCardMirrorsTheLastView(t *testing.T) {
	card := NewCard(CardSuccess, "original")
	successor := NewCard(CardSuccess, "successor")
	boom := fmt.Errorf("boom")

	if got := finalFrameCard(card, func() *Card { return successor }, nil); got != successor {
		t.Error("success with a successor should record the successor")
	}
	if got := finalFrameCard(card, func() *Card { return successor }, boom); got != card {
		t.Error("failure should record the original card, not the successor")
	}
	if got := finalFrameCard(card, nil, nil); got != card {
		t.Error("success without a successor should record the original card")
	}
}

// TestFailedStepIndexFindsTheHaltedStep: RunCardSteps records whatever
// its program left on screen, which after a halt is the card the model
// flipped out of CardRunning.
func TestFailedStepIndexFindsTheHaltedStep(t *testing.T) {
	steps := []CardStep{
		{Card: NewCard(CardRunning, "one")},
		{Card: NewCard(CardFailed, "two")},
		{Card: NewCard(CardRunning, "three")},
	}
	if got := failedStepIndex(steps); got != 1 {
		t.Errorf("failedStepIndex = %d, want 1", got)
	}

	// Nothing flipped (the worker was cancelled between steps): fall
	// back to the last card rather than indexing out of range.
	allRunning := []CardStep{
		{Card: NewCard(CardRunning, "one")},
		{Card: NewCard(CardRunning, "two")},
	}
	if got := failedStepIndex(allRunning); got != 1 {
		t.Errorf("failedStepIndex with no halted step = %d, want 1", got)
	}
}

// rewritePattern matches the timeline's cursor-up + clear-to-end
// escape and captures the line count. Deliberately narrow: SGR color
// codes share the CSI prefix, and treating one as a cursor move would
// silently corrupt the replay.
var rewritePattern = regexp.MustCompile(`\x1b\[(\d+)F\x1b\[J`)

// replayRewrites applies those escapes to a captured stream and
// returns what the terminal would end up showing — content between
// escapes is appended as rows, and each escape drops that many rows
// off the end (clear-to-end always reaches the bottom, since the
// timeline only ever rewrites its own tail).
func replayRewrites(out string) string {
	var screen []string
	for {
		loc := rewritePattern.FindStringSubmatchIndex(out)
		if loc == nil {
			screen = append(screen, splitLines(out)...)
			break
		}
		screen = append(screen, splitLines(out[:loc[0]])...)
		up, err := strconv.Atoi(out[loc[2]:loc[3]])
		if err != nil || up > len(screen) {
			up = len(screen)
		}
		screen = screen[:len(screen)-up]
		out = out[loc[1]:]
	}
	return strings.Join(screen, "\n")
}

// splitLines splits on newlines, dropping the empty tail a trailing
// newline produces so appends land on the next row rather than
// creating a phantom one.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func trimTrailingNewline(s string) string { return strings.TrimSuffix(s, "\n") }

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
