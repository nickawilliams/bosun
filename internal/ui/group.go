package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
)

// RunGroup renders a Timeline Card with children. The parent header
// prints with an animated spinner; each child emitted from within fn
// renders indented under it in real-time. When fn returns, the parent's
// spinner is replaced with a glyph aggregated from its children:
//
//   - any failure → failure
//   - all skipped → skipped
//   - any success (including success+skipped mix) → success
//   - info-status emissions don't propagate to the aggregate
//
// The inner Reporter passed to fn is scoped to this group: its
// emissions are tracked for aggregation and indented under the
// parent. Nested RunGroup calls work — each level adds another
// 4 spaces of indent.
//
// Constraint: the parent header is rendered as a single-line title
// card (no subtitle/body) so the rewind math stays simple. If a
// future use case needs a multi-line parent, the rewind logic in
// finalize must be extended.
func RunGroup(title string, fn func(g Reporter)) {
	defaultReporter.Group(title, fn)
}

// group is the internal Reporter scope used by RunGroup. It wraps an
// outer Reporter, shifts every emission to indented child rendering,
// and tallies outcomes for the parent's aggregate state.
type group struct {
	outer       Reporter
	title       string
	indent      int // children render at this indent depth
	parentLines int // how many terminal lines the parent header occupied
	childLines  int // running total of lines emitted by children
	counts      groupCounts
	spinner     *groupSpinner
	finalized   bool
}

type groupCounts struct {
	success int
	skipped int
	failed  int
	info    int
}

// beginGroup emits the parent with an animated spinner and returns a
// group scope. indentLevel is the parent's indent depth (0 for
// top-level, n for nested). The children render at indentLevel+1.
func beginGroup(outer Reporter, title string, indentLevel int) *group {
	parent := NewCard(CardPending, title).Indent(indentLevel)

	// Print comfy prefix separately — it doesn't participate in the
	// rewind math. Only the parent content line(s) get erased and
	// reprinted during finalize.
	fmt.Print(comfyPrefix())

	contentRender := parent.Render()
	fmt.Print(contentRender)

	// Children are tight relative to the parent: no comfy break before
	// the first child.
	comfyBreak = false
	contentLines := strings.Count(contentRender, "\n")

	// Glyph column: 1-space pad + indent*4 + 1 (for 1-indexed column).
	glyphCol := indentLevel*4 + 2

	g := &group{
		outer:       outer,
		title:       title,
		indent:      indentLevel + 1,
		parentLines: contentLines, // content only, excludes comfy prefix
	}
	g.spinner = newGroupSpinner(glyphCol, &g.childLines)
	return g
}

// finalize stops the parent spinner, rewinds the pending content
// line, and reprints it in the aggregate final state, leaving
// children in place. Cursor is restored to the line after the last
// child so subsequent timeline output continues normally.
//
// Only the content line(s) are rewritten — any comfy prefix printed
// before the parent by beginGroup is left untouched.
func (g *group) finalize() {
	if g.finalized {
		return
	}
	g.finalized = true
	g.spinner.stop()

	finalState := g.aggregate()
	finalParent := NewCard(finalState, g.title).Indent(g.indent - 1)
	finalRender := finalParent.Render()
	finalLines := strings.Count(finalRender, "\n")

	// Conservative fallback: if we somehow can't rewind in place
	// (line count mismatch or no parent printed), append below.
	if g.parentLines == 0 || finalLines != g.parentLines {
		fmt.Print(finalRender)
		comfyBreak = !finalParent.tight
		return
	}

	// The parent content starts parentLines + childLines above the
	// cursor (which sits on the line after the last child).
	linesUp := g.parentLines + g.childLines
	if linesUp > 0 {
		fmt.Printf("\x1b[%dF", linesUp)
	}

	// Erase and reprint the content line(s). For single-line parents
	// (the common case) this is one erase + one print.
	for i := 0; i < g.parentLines; i++ {
		fmt.Print("\x1b[2K")
		if i < g.parentLines-1 {
			fmt.Print("\n")
		}
	}
	if g.parentLines > 1 {
		fmt.Printf("\x1b[%dF", g.parentLines-1)
	} else {
		fmt.Print("\r")
	}

	fmt.Print(finalRender)

	// Move down past the children to the original post-output position.
	if g.childLines > 0 {
		fmt.Printf("\x1b[%dB\r", g.childLines)
	}

	comfyBreak = true
}

// aggregate computes the parent's final state from child outcomes.
// Info-only groups (no success/skip/fail) collapse to success rather
// than info, because info doesn't propagate to a parent's status —
// a group with only info children represents work that ran
// without producing a tracked outcome.
func (g *group) aggregate() CardState {
	if g.counts.failed > 0 {
		return CardFailed
	}
	if g.counts.success > 0 {
		return CardSuccess
	}
	if g.counts.skipped > 0 {
		return CardSkipped
	}
	return CardSuccess
}

// emit prints a child card under this group, applying group indent
// and tightness, and bumps the appropriate outcome counter.
func (g *group) emit(c *Card, outcome CardState) {
	c.Indent(g.indent)
	c.tight = true
	rendered := c.Render()
	g.spinner.withPause(func() {
		fmt.Print(rendered)
		g.childLines += strings.Count(rendered, "\n")
	})
	switch outcome {
	case CardSuccess:
		g.counts.success++
	case CardSkipped:
		g.counts.skipped++
	case CardFailed:
		g.counts.failed++
	case CardInfo:
		g.counts.info++
	}
}

// --- Reporter implementation for group scope ---

func (g *group) Header(_ string, _ ...string) {
	// Headers are root-level; ignore inside a group scope.
}

func (g *group) Complete(label string) {
	g.emit(NewCard(CardSuccess, label), CardSuccess)
}

func (g *group) CompleteDetail(label string, items []string) {
	g.emit(NewCard(CardSuccess, label).Muted(items...), CardSuccess)
}

func (g *group) Skip(label string) {
	g.emit(NewCard(CardSkipped, label), CardSkipped)
}

func (g *group) Fail(label string) {
	g.emit(NewCard(CardFailed, label), CardFailed)
}

func (g *group) Success(format string, args ...any) {
	g.emit(NewCard(CardSuccess, fmt.Sprintf(format, args...)), CardSuccess)
}

func (g *group) Warning(format string, args ...any) {
	g.emit(NewCard(CardSkipped, fmt.Sprintf(format, args...)), CardSkipped)
}

func (g *group) Info(format string, args ...any) {
	g.emit(NewCard(CardInfo, fmt.Sprintf(format, args...)), CardInfo)
}

func (g *group) Muted(format string, args ...any) {
	// Muted is a styling helper; treat as info inside a group so it
	// participates in the rendered timeline at the right indent.
	g.emit(NewCard(CardInfo, fmt.Sprintf(format, args...)), CardInfo)
}

func (g *group) DryRun(format string, args ...any) {
	g.emit(NewCard(CardInfo, fmt.Sprintf("[dry-run] "+format, args...)), CardInfo)
}

func (g *group) Saved(label, value string) {
	g.emit(NewCard(CardSuccess, label).Muted(value), CardSuccess)
}

func (g *group) Selected(label, value string) {
	g.emit(NewCard(CardSuccess, label).Subtitle(value), CardSuccess)
}

func (g *group) SelectedMulti(label string, values []string) {
	if len(values) == 0 {
		g.emit(NewCard(CardSuccess, label).Subtitle("(none)"), CardSuccess)
		return
	}
	g.emit(NewCard(CardSuccess, label).Muted(values...), CardSuccess)
}

func (g *group) Task(title string, fn func() error) error {
	card := NewCard(CardRunning, title).Indent(g.indent)
	card.tight = true
	// Pause parent spinner while the child's BubbleTea spinner runs —
	// two concurrent cursor-managing programs would corrupt the display.
	g.spinner.stop()
	err := runCardWith(card, fn)
	// runCardWith printed the finalized card; its state reflects outcome.
	finalRender := card.Render()
	g.childLines += strings.Count(finalRender, "\n")
	if err != nil {
		g.counts.failed++
	} else {
		g.counts.success++
	}
	g.spinner.start()
	return err
}

func (g *group) Details(heading string, fields Fields) {
	pairs := make([]string, 0, len(fields)*2)
	for _, f := range fields {
		pairs = append(pairs, f.Key, f.Value)
	}
	if heading == "" {
		heading = "Details"
	}
	g.emit(NewCard(CardInfo, heading).KV(pairs...), CardInfo)
}

func (g *group) Table(columns ...Column) *Table {
	// Table renders directly to stdout via lipgloss/table and isn't
	// indent-aware. Returning the outer Reporter's table preserves
	// the API; if Tables-in-Groups becomes a real use case the
	// indent plumbing will need to land there too.
	return g.outer.Table(columns...)
}

// Group nesting: forwarding into another group bumps the indent
// depth so deeper children render further indented.
func (g *group) Group(title string, fn func(Reporter)) {
	// Pause our spinner while the child group runs — the child group
	// starts its own spinner and manages its own cursor.
	g.spinner.stop()
	child := beginGroup(g.outer, title, g.indent)
	startChildLines := g.childLines
	fn(child)
	child.finalize()
	g.childLines = startChildLines + child.parentLines + child.childLines
	g.spinner.start()
}

// --- Parent spinner ---
//
// groupSpinner animates the parent card's glyph using a background
// goroutine and relative ANSI cursor movements. It pauses when a
// child BubbleTea spinner runs (to avoid conflicting cursor control)
// and resumes afterward.

// miniDotFrames matches the MiniDot spinner used by card spinners so
// parent and child animations look consistent.
var miniDotFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type groupSpinner struct {
	mu         sync.Mutex
	glyphCol   int  // 1-indexed terminal column of the glyph
	childLines *int // pointer to group's running child-line count

	frameIdx int
	ticker   *time.Ticker
	stopCh   chan struct{}
	doneCh   chan struct{} // closed when loop goroutine exits
	running  bool
}

func newGroupSpinner(glyphCol int, childLines *int) *groupSpinner {
	gs := &groupSpinner{
		glyphCol:   glyphCol,
		childLines: childLines,
	}
	gs.start()
	return gs
}

func (gs *groupSpinner) start() {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if gs.running {
		return
	}
	gs.running = true
	gs.ticker = time.NewTicker(80 * time.Millisecond)
	gs.stopCh = make(chan struct{})
	gs.doneCh = make(chan struct{})
	go gs.loop()
}

func (gs *groupSpinner) stop() {
	gs.mu.Lock()
	if !gs.running {
		gs.mu.Unlock()
		return
	}
	gs.running = false
	gs.ticker.Stop()
	close(gs.stopCh)
	doneCh := gs.doneCh
	gs.mu.Unlock()
	// Block until the goroutine exits so no stray frame writes
	// leak after stop returns.
	<-doneCh
}

func (gs *groupSpinner) loop() {
	defer close(gs.doneCh)
	style := lipgloss.NewStyle().Foreground(Palette.Primary)
	for {
		select {
		case <-gs.stopCh:
			return
		case <-gs.ticker.C:
			gs.mu.Lock()
			frame := style.Render(miniDotFrames[gs.frameIdx])
			gs.frameIdx = (gs.frameIdx + 1) % len(miniDotFrames)
			// The glyph is always on the last line of the parent
			// render (the content line, after any comfy prefix).
			// From the current cursor position (line after last
			// child), that's childLines + 1 lines up.
			linesUp := *gs.childLines + 1
			// CUU up, CHA to glyph column, write frame, CUD back
			// down, CR to col 1. Relative movements are more
			// reliable than DEC save/restore across terminal scroll.
			fmt.Printf("\x1b[%dA\x1b[%dG%s\x1b[%dB\r",
				linesUp, gs.glyphCol, frame, linesUp)
			gs.mu.Unlock()
		}
	}
}

// withPause stops the spinner, runs fn (which writes to stdout),
// then restarts the spinner. This prevents ANSI cursor movement
// from the spinner interleaving with child output writes.
func (gs *groupSpinner) withPause(fn func()) {
	gs.stop()
	fn()
	gs.start()
}
