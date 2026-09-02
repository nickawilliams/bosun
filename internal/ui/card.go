package ui

import (
	_ "embed"
	"fmt"
	"image/color"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CardState represents the lifecycle state of a card.
//
// Semantic guide for command output:
//   - CardSuccess — operation completed successfully (terminal good)
//   - CardReady   — operation is complete pending a user action (e.g.,
//     a PR is mergeable but not yet merged)
//   - CardFailed  — operation was attempted and returned an error
//   - CardSkipped — operation was not attempted (missing config, optional
//     dependency unavailable, precondition unmet). Also doubles as
//     the "blocked" state in aggregate status output — the glyph
//     and color (▲ warning) read the same way regardless of whether
//     the cause is "didn't run" or "needs you to act."
//   - CardWaiting — operation is in progress and the ball is not in
//     the user's court (CI running, PR under review, draft sitting)
//   - CardInfo    — informational display, not an operation result
//   - CardInput   — interactive prompt (use with PrintRewindable)
//   - CardRoot    — command header (timeline anchor)
//   - CardPending/CardRunning — transient states used by spinners
type CardState int

const (
	CardPending CardState = iota
	CardRunning
	CardSuccess
	CardSkipped
	CardFailed
	CardInfo
	CardInput
	CardRoot
	CardData    // structured state snapshot, no status glyph
	CardReady   // ● — terminal good pending a user action
	CardWaiting // ⧗ — in progress, not on the user

	// cardStateCount is the sentinel that bounds the enum. Keep it
	// last: tests iterate up to it to prove every state resolves to a
	// glyph, so a state appended after it would escape that check.
	cardStateCount
)

// cardConnector is the vertical spine drawn down the left gutter
// between a card's glyph row and its continuation lines.
const cardConnector = BoxVertical

// stateGlyph returns the unstyled glyph and its natural color for a
// card state. Shapes come from the palette's symbol vocabulary so a
// glyph mode swaps every card at once; the pairings follow the event
// grammar in state_grammar.go.
//
// The third return reports whether the state renders a glyph at all —
// unrecognized states fall through to a blank gutter.
func stateGlyph(s CardState) (glyph string, fg color.Color, ok bool) {
	switch s {
	case CardPending:
		return Palette.Pending, Palette.Muted, true
	case CardRunning:
		return Palette.Pending, Palette.Primary, true
	case CardSuccess:
		return Palette.Check, Palette.Success, true
	case CardSkipped:
		return Palette.Attention, Palette.Warning, true
	case CardFailed:
		return Palette.Cross, Palette.Error, true
	case CardInfo, CardData:
		return Palette.Active, Palette.Primary, true
	case CardInput:
		return Palette.Unknown, Palette.Accent, true
	case CardRoot:
		// The top-left rounded corner anchors the timeline: the
		// corner turns into the vertical spine that runs through
		// every card below.
		return BoxCornerTL, Palette.Recessed, true
	case CardReady:
		// ● not ✓ — the work is good but not finished, so it keeps
		// the active shape and borrows the success color.
		return Palette.Active, Palette.Success, true
	case CardWaiting:
		return Palette.Waiting, Palette.Info, true
	}
	return "", nil, false
}

// AppVersion is the application version displayed in the upper-right
// corner of the root card box. Set from main via ldflags.
var AppVersion = "dev"

// BreadcrumbPrefix is an optional glyph rendered before the breadcrumb
// title on the root card closing line. Leave empty to omit.
var BreadcrumbPrefix = ""

// BreadcrumbPostfix is an optional glyph rendered after the breadcrumb
// title, before the trailing rule on the root card closing line.
// Leave empty to omit.
var BreadcrumbPostfix = ""

//go:embed logo.txt
var logoRaw string

// asciiLogo is the block-character art rendered in place of the
// plain "Bosun" text on root cards. The content is embedded from
// logo.txt at build time — edit that file to change the art.
var asciiLogo = func() []string {
	lines := strings.Split(strings.TrimSuffix(logoRaw, "\n"), "\n")
	return lines
}()

// Card represents a single unit of output in the bosun timeline.
// Cards render with a state glyph in the left gutter and one or more
// content slots (title, subtitle, body) to the right. Continuation
// lines are drawn with a muted connector so a run of cards reads as
// a single vertical timeline.
type Card struct {
	state         CardState
	title         string
	titleColor    color.Color // optional override for the title foreground (default: Palette.Primary)
	glyphColor    color.Color // optional override for the gutter glyph color (default: per-state)
	value         string      // Rendered after title as-is (no title-casing), muted style.
	subtitle      string
	body          []cardBody
	tight         bool // suppress comfy spacing (e.g. single-field prompts)
	indent        int  // additional left-margin depth (1 = +4 spaces); used by Group children
	preserveTitle bool // skip the default titleCase transform on the title
	plainTitle    bool // render the title without bold (group children)
	alignWidth    int  // pad styled title to this visual width before " · " when Value is set; 0 = natural
	accentBody    bool // render body-line connectors in Palette.Accent rather than the default Palette.Recessed

	// breadcrumb is the root-header component. Non-nil only for
	// CardRoot; lazy-initialized when any breadcrumb-related
	// builder method is called. Owns segments.
	breadcrumb *breadcrumb
}

type cardBodyKind int

const (
	cardBodyText cardBodyKind = iota
	cardBodyMuted
	cardBodyKV
	cardBodyStdout
	cardBodyStderr
	cardBodyRaw   // pre-styled lines, no additional formatting
	cardBodyItem  // glyph + content rows, indented under the title
	cardBodyTable // arbitrary-column tabular data, columns auto-aligned
)

type cardBody struct {
	kind  cardBodyKind
	lines []string
	pairs [][2]string // used by cardBodyKV
	items []cardItem  // used by cardBodyItem
	table [][]string  // used by cardBodyTable; each inner slice is a row of pre-styled cells
}

// cardItem is one glyph+content row in a card body. Both fields
// are pre-styled (may contain ANSI escapes); the renderer adds no
// further styling, only spacing.
type cardItem struct {
	glyph   string
	content string
}

// NewCard creates a card with the given state and title. A title
// wrapped with ui.PreserveCase keeps its original casing (same
// sentinel convention the cli's form-field constructors honor) —
// the escape hatch for identifier-shaped titles (repo names, slugs)
// flowing through Reporter methods that don't otherwise expose a
// PreserveCase toggle, e.g. group children via CompleteValue.
func NewCard(state CardState, title string) *Card {
	clean, verbatim := StripPreserveCase(title)
	return &Card{state: state, title: clean, preserveTitle: verbatim}
}

// Tight suppresses the comfy-mode timeline padding after this card.
// Use for single-field prompts where a huh form renders immediately
// below without a visual gap.
func (c *Card) Tight() *Card {
	c.tight = true
	return c
}

// Indent shifts the card's rendering right by n*4 spaces. Used by
// Group to nest children under a parent's spine.
func (c *Card) Indent(n int) *Card {
	c.indent = n
	return c
}

// PreserveCase suppresses the default title-case transform on the
// card title. Use for identifier-like titles (repo names, paths,
// URLs) where the input casing is meaningful.
func (c *Card) PreserveCase() *Card {
	c.preserveTitle = true
	return c
}

// TitleColor overrides the foreground color used for the card
// title. Default is Palette.Primary (the branding indigo). Pass a
// different color to tint the title — e.g., to match the state
// glyph color so a card's identity reads at the same hue as its
// status indicator.
func (c *Card) TitleColor(col color.Color) *Card {
	c.titleColor = col
	return c
}

// GlyphColor overrides the foreground color used for the card's
// gutter glyph. Default is the state's natural color (e.g., success
// green for CardSuccess). Pass a different color to keep the
// glyph shape from the state but recolor it — e.g., a muted
// summary recap that uses the CardInfo bullet in muted gray.
func (c *Card) GlyphColor(col color.Color) *Card {
	c.glyphColor = col
	return c
}

// Breadcrumb appends a data segment to this card's breadcrumb.
// Only meaningful for CardRoot; lazy-initializes the breadcrumb
// component on first call. Use when the segment value is known
// synchronously at card construction — the segment renders inline
// with the title on first paint.
//
// Empty strings are ignored. Style is fixed by the breadcrumb
// component (data segments use Palette.Success).
func (c *Card) Breadcrumb(s string) *Card {
	if s == "" {
		return c
	}
	if c.breadcrumb == nil {
		c.breadcrumb = &breadcrumb{}
	}
	c.breadcrumb.AddSegment(s)
	return c
}

// Value sets an inline value rendered after the title, separated by
// a muted middle-dot. The value is not title-cased and uses muted
// non-bold style. When the value contains newlines, the first line
// renders inline and subsequent lines indent to align under the
// first value character.
func (c *Card) Value(s string) *Card {
	c.value = s
	return c
}

// AlignWidth sets a target visual column width for the title when
// combined with Value. The title is padded with spaces so the " · "
// separator (and the first character of the value) lines up with
// sibling cards using the same alignment width. If the title is
// already wider than w, alignment has no effect on that card. Zero
// (the default) means natural title width — no padding.
func (c *Card) AlignWidth(w int) *Card {
	c.alignWidth = w
	return c
}

// AccentBody flips the body-line connector color from the default
// recessed timeline gray to Palette.Accent. Use when this card is
// the heading of a single-input active prompt and the card body +
// the form below it should read as one continuous accent-colored
// section (e.g., Dialog confirmations). Multi-input forms should
// leave this default so only the focused field renders in accent.
func (c *Card) AccentBody() *Card {
	c.accentBody = true
	return c
}

// Subtitle sets a muted subtitle line (context, ID, path).
func (c *Card) Subtitle(s string) *Card {
	c.subtitle = s
	return c
}

// Text appends raw text body lines in the default foreground.
func (c *Card) Text(lines ...string) *Card {
	c.body = append(c.body, cardBody{kind: cardBodyText, lines: lines})
	return c
}

// Muted appends dimmed/secondary body lines.
func (c *Card) Muted(lines ...string) *Card {
	c.body = append(c.body, cardBody{kind: cardBodyMuted, lines: lines})
	return c
}

// KV appends a key-value body block. Arguments must be pairs of
// strings: key1, value1, key2, value2, ...
func (c *Card) KV(pairs ...string) *Card {
	kv := make([][2]string, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		kv = append(kv, [2]string{pairs[i], pairs[i+1]})
	}
	c.body = append(c.body, cardBody{kind: cardBodyKV, pairs: kv})
	return c
}

// Raw appends pre-styled body lines without additional formatting.
// Use when lines contain embedded ANSI codes that must be preserved.
func (c *Card) Raw(lines ...string) *Card {
	c.body = append(c.body, cardBody{kind: cardBodyRaw, lines: lines})
	return c
}

// Item appends a glyph+content row to the body. The row renders
// with │ at col 2, glyph at col 6, content at col 9 — aligning
// with Group child glyphs (which sit one column right of the
// parent's │ + 2-space gap). Use for body rows that carry their
// own per-row state glyph (status repo rows, plan items, etc.).
// Both arguments are pre-styled.
//
// Calling Item multiple times accumulates rows in the order given.
func (c *Card) Item(glyph, content string) *Card {
	if n := len(c.body); n > 0 && c.body[n-1].kind == cardBodyItem {
		c.body[n-1].items = append(c.body[n-1].items, cardItem{glyph, content})
		return c
	}
	c.body = append(c.body, cardBody{
		kind:  cardBodyItem,
		items: []cardItem{{glyph, content}},
	})
	return c
}

// Table appends a tabular body block. Each row is a slice of
// pre-styled cells; the renderer auto-pads each column to its
// widest cell (measured by visible width, ANSI-aware) and joins
// columns with a 2-space gap. Rows with fewer cells than the
// widest row pad with empty strings.
//
// Use for dimensional metadata where rows describe attributes
// (Branch, PR, etc.) and columns are label/state/value rather
// than a sequential list. Calling Table multiple times appends
// distinct table blocks.
func (c *Card) Table(rows ...[]string) *Card {
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = append([]string{}, r...)
	}
	c.body = append(c.body, cardBody{kind: cardBodyTable, table: cells})
	return c
}

// Stdout appends stdout stream lines (muted).
func (c *Card) Stdout(lines ...string) *Card {
	c.body = append(c.body, cardBody{kind: cardBodyStdout, lines: lines})
	return c
}

// Stderr appends stderr stream lines (error color).
func (c *Card) Stderr(lines ...string) *Card {
	c.body = append(c.body, cardBody{kind: cardBodyStderr, lines: lines})
	return c
}

// Render returns the card as a styled multi-line string ending in a
// newline, in OPEN form: no timeline spine on its body lines. A card
// is rendered at the moment it becomes the tail of the timeline, and
// a spine there would promise content that doesn't exist yet. Once a
// successor prints, the timeline rewrites the card via
// renderContinuing — see timeline.go.
func (c *Card) Render() string {
	return c.renderForm(formOpen)
}

// renderContinuing returns the card in CONTINUING form: body lines
// carry the timeline spine, because something is printed below it.
// This is the shape every card had before the open/continuing swap
// existed, and the shape every card ends up in except the last.
func (c *Card) renderContinuing() string {
	return c.renderForm(formContinuing)
}

// renderForm renders the card in the given form with its state glyph.
func (c *Card) renderForm(form timelineForm) string {
	glyph, gap := c.glyphFor(form)
	return c.renderStyled(glyph, gap, form)
}

// renderBreadcrumbRow delegates to the breadcrumb component. Lazy-
// inits an empty breadcrumb if needed (so a root with no segments
// still renders its closing rule). In compact header mode, renders
// the single-line header including all command segments and the
// version string. In logo mode, renders just the bottom row of the
// logo box.
func (c *Card) renderBreadcrumbRow() string {
	if c.state != CardRoot {
		return ""
	}
	bc := c.breadcrumb
	if bc == nil {
		bc = &breadcrumb{}
	}
	titleSegs := strings.Split(c.title, " › ")
	if IsCompactHeader() {
		return bc.RenderCompactRow(TermWidth(), titleSegs)
	}
	boxInner := TermWidth() - 3
	if boxInner < 10 {
		boxInner = 10
	}
	var commandTail []string
	if len(titleSegs) > 1 {
		commandTail = titleSegs[1:]
	}
	return bc.RenderRow(boxInner, commandTail)
}

// Print writes the card to stdout in open form and becomes the
// timeline's new tail: spacerPrefix rewrites whatever card was open
// before into continuing form, then this one takes its place.
// Suppressed in raw mode.
func (c *Card) Print() {
	if IsRaw() {
		return
	}
	if s := sessionActive(); s != nil {
		s.print(c.Render(), c.renderContinuing(), c.tight)
		return
	}
	rendered := c.Render()
	fmt.Print(spacerPrefix() + rendered)
	if c.tight {
		ClearSpacer()
	}
	recordOpenCard(rendered, c.renderContinuing())
}

// EmitToReporter routes the card's completion state through the
// Reporter interface. Used in raw-mode paths where Card.Print() is
// suppressed: plainReporter emits a plain-text line; rawReporter stays
// silent (correct for machine-readable mode). CardInput and spinner
// states (running, pending) are intentionally omitted. CardWaiting is
// a terminal semantic state ("CI running, PR under review") and maps
// to Info.
//
// Subtitle takes priority over Value when both are set on terminal-state
// cards — the subtitle is the human-readable annotation in most
// successor cards (e.g. "✓ preview · env-name").
func (c *Card) EmitToReporter(r Reporter) {
	switch c.state {
	case CardSuccess, CardReady:
		switch {
		case c.subtitle != "":
			r.CompleteValue(c.title, c.subtitle)
		case c.value != "":
			r.CompleteValue(c.title, c.value)
		default:
			r.Complete(c.title)
		}
	case CardFailed:
		switch {
		case c.subtitle != "":
			r.FailValue(c.title, c.subtitle)
		case c.value != "":
			r.FailValue(c.title, c.value)
		default:
			r.Fail(c.title)
		}
	case CardSkipped:
		switch {
		case c.subtitle != "":
			r.SkipValue(c.title, c.subtitle)
		case c.value != "":
			r.SkipValue(c.title, c.value)
		default:
			r.Skip(c.title)
		}
	case CardInfo, CardData, CardWaiting:
		r.Info("%s", c.title)
	}
}

// PrintRewindable writes the card to stdout and returns a function
// that, when called, erases the card by moving the cursor back to
// its first row and clearing from there to the end of the screen.
// Suppressed in raw mode (returns a no-op rewind).
func (c *Card) PrintRewindable() func() {
	if IsRaw() {
		return func() {}
	}
	if s := sessionActive(); s != nil {
		rec := s.print(c.Render(), c.renderContinuing(), c.tight)
		return s.sessionRewind(rec)
	}
	prev := needsSpacer
	card := c.Render()
	rendered := spacerPrefix() + card
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	if c.tight {
		ClearSpacer()
	}
	rec := recordOpenCard(card, c.renderContinuing())
	return func() {
		if lines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
		needsSpacer = prev
		// The block this record pointed at no longer exists; rewriting
		// it would move the cursor over live content above.
		discardRecord(rec)
	}
}

// recordCard marks c as the timeline's open card, re-deriving what it
// painted from its own render. Use after a card reached the screen by
// some route other than Print — most often as a BubbleTea program's
// final frame, which paints the card but leaves the timeline none the
// wiser.
func recordCard(c *Card) *openCardRecord {
	return recordOpenCard(c.Render(), c.renderContinuing())
}

// GlyphSlot is a placeholder for a body-item glyph that should track
// the card's live glyph at render time — the animated spinner frame
// while the card is running, the state glyph otherwise. Gather cards
// use it to put the spinner on the in-flight row (the item being
// resolved) while completed rows keep their final glyphs. U+E000
// (private use) can't collide with real content.
const GlyphSlot = "\uE000"

// renderWithGlyph renders the card with a custom leading glyph.
// Used by the spinner to animate the state indicator in place.
// Always open form: an animating card is by definition the tail of
// the timeline, and so is the final frame the program leaves behind.
func (c *Card) renderWithGlyph(glyph string) string {
	return c.renderStyled(glyph, strings.Repeat(" ", GlyphGap), formOpen)
}

// renderStyled is the shared render path: glyph and gap already
// resolved, form deciding the body-line gutter.
func (c *Card) renderStyled(glyph, gap string, form timelineForm) string {
	// v1 boundary (issue #27): only top-level cards participate in
	// the open/continuing swap. A nested card's body sits inside its
	// parent's spine, so dropping its own connector would punch a hole
	// in the middle of a group rather than terminate anything.
	if c.indent > 0 {
		form = formContinuing
	}
	out := c.renderInner(glyph, gap, form)
	if strings.Contains(out, GlyphSlot) {
		out = strings.ReplaceAll(out, GlyphSlot, glyph)
	}
	if c.indent <= 0 {
		return out
	}
	// Build an indent prefix that continues the parent's timeline
	// spine at each nesting level: " │  " per level. This keeps
	// the vertical connector visible through nested children
	// instead of leaving a blank gap.
	connStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
	var prefix string
	for range c.indent {
		prefix += " " + connStyle.Render(cardConnector) + "  "
	}
	trimmed := strings.TrimSuffix(out, "\n")
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderInner is the indent-agnostic render path.
func (c *Card) renderInner(glyph, gap string, form timelineForm) string {
	const pad = " "
	conn := c.bodyPrefix(form)

	// CardRoot has its own rendering path (logo box / compact header).
	if c.state == CardRoot {
		return c.renderRoot(glyph, pad, conn)
	}

	// Build a flat list of content lines from title, subtitle, body.
	// The first line renders next to the glyph; the rest get connector
	// prefixes. Missing elements are simply absent from the list, so
	// empty titles collapse naturally without special cases.
	var lines []string

	// Title line (with optional inline value).
	titleFg := Palette.Primary
	if c.titleColor != nil {
		titleFg = c.titleColor
	}
	titleStyle := lipgloss.NewStyle().Bold(!c.plainTitle).Foreground(titleFg)
	if !c.preserveTitle {
		titleStyle = titleStyle.Transform(titleCase)
	}

	if c.value != "" {
		valueStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		titleRendered := titleStyle.Render(c.title)

		// Title column alignment: when c.alignWidth is set and exceeds
		// the title's natural visual width, pad the styled title with
		// spaces so the " · " separator lines up with sibling cards.
		naturalW := lipgloss.Width(titleRendered)
		alignW := c.alignWidth
		if alignW < naturalW {
			alignW = naturalW
		}
		if alignW > naturalW {
			titleRendered += strings.Repeat(" ", alignW-naturalW)
		}

		// Multi-line value support: first line renders inline after
		// the title separator; subsequent lines align under the first
		// value character (4-char conn prefix is added by the outer
		// loop, so we add alignW + 3 spaces here so total leading
		// padding matches the first line's value column). Overlong
		// lines wrap HERE, to the width left of that column, so every
		// fragment keeps it — these lines never pass through
		// wrapForTimeline, and a physical terminal wrap would break
		// both the gutter and the rewind's line count.
		valueWidth := TermWidth() - timelineConnWidth - alignW - 3
		if valueWidth < 20 {
			valueWidth = 20
		}
		contPad := strings.Repeat(" ", alignW+3)
		first := true
		for _, logical := range strings.Split(c.value, "\n") {
			frags := []string{logical}
			if lipgloss.Width(logical) > valueWidth {
				frags = strings.Split(lipgloss.Wrap(logical, valueWidth, " ,.-"), "\n")
			}
			for _, frag := range frags {
				if first {
					lines = append(lines, titleRendered+valueStyle.Render(" · "+frag))
					first = false
					continue
				}
				lines = append(lines, contPad+valueStyle.Render(frag))
			}
		}
	} else if c.title != "" {
		lines = append(lines, titleStyle.Render(c.title))
	}

	// Subtitle lines.
	if c.subtitle != "" {
		subtitleStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		for _, line := range wrapForTimeline(c.subtitle) {
			lines = append(lines, subtitleStyle.Render(line))
		}
	}

	// Body lines.
	kvWidth := maxKVKeyWidth(c.body)
	for _, body := range c.body {
		for _, line := range renderCardBody(body, kvWidth) {
			lines = append(lines, wrapForTimeline(line)...)
		}
	}

	// Render: first line next to glyph, rest with connector prefix.
	var b strings.Builder
	if len(lines) == 0 {
		fmt.Fprintf(&b, "%s%s\n", pad, glyph)
	} else {
		fmt.Fprintf(&b, "%s%s%s%s\n", pad, glyph, gap, lines[0])
		for _, line := range lines[1:] {
			fmt.Fprintf(&b, "%s%s\n", conn, line)
		}
	}
	return b.String()
}

// renderRoot handles the CardRoot-specific logo box and compact header.
func (c *Card) renderRoot(glyph, pad, conn string) string {
	var b strings.Builder

	if IsCompactHeader() {
		b.WriteString(c.renderBreadcrumbRow())
	} else {
		ruleStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
		// 1 pad left + 1 border + content + 1 border + 1 pad right = 4 chrome cols.
		boxInner := TermWidth() - 4
		if boxInner < 10 {
			boxInner = 10
		}

		fmt.Fprintf(&b, "%s%s%s \n", pad, glyph,
			ruleStyle.Render(strings.Repeat(BoxHorizontal, boxInner)+BoxCornerTR))

		versionStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		versionStr := versionStyle.Render(AppVersion)
		versionWidth := lipgloss.Width(versionStr)

		// Vertical gradient from LogoTop to LogoBottom.
		logoColors := lerpColors(Palette.LogoTop, Palette.LogoBottom, len(asciiLogo))

		for i, line := range asciiLogo {
			lineStyle := lipgloss.NewStyle().Bold(true).Foreground(logoColors[i])
			artWidth := lipgloss.Width(line)
			if i == 0 {
				rightPad := boxInner - 2 - artWidth - versionWidth - 2
				if rightPad < 1 {
					rightPad = 1
				}
				fmt.Fprintf(&b, "%s%s  %s%s%s  %s \n", pad,
					ruleStyle.Render(BoxVertical),
					lineStyle.Render(line),
					strings.Repeat(" ", rightPad),
					versionStr,
					ruleStyle.Render(BoxVertical))
			} else {
				rightPad := boxInner - 2 - artWidth
				if rightPad < 1 {
					rightPad = 1
				}
				fmt.Fprintf(&b, "%s%s  %s%s%s \n", pad,
					ruleStyle.Render(BoxVertical),
					lineStyle.Render(line),
					strings.Repeat(" ", rightPad),
					ruleStyle.Render(BoxVertical))
			}
		}

		b.WriteString(c.renderBreadcrumbRow())
	}

	// Root body (rare — used by demo cards with body on root).
	if len(c.body) > 0 {
		fmt.Fprintf(&b, "%s\n", conn)
		kvWidth := maxKVKeyWidth(c.body)
		for _, body := range c.body {
			for _, line := range renderCardBody(body, kvWidth) {
				for _, wrapped := range wrapForTimeline(line) {
					fmt.Fprintf(&b, "%s%s\n", conn, wrapped)
				}
			}
		}
	}

	return b.String()
}

// glyph returns the styled state glyph for this card. When
// Card.GlyphColor has been set, it overrides the state's natural
// color while preserving the glyph shape.
func (c *Card) glyph() string {
	ch, fg, ok := stateGlyph(c.state)
	if !ok {
		return " "
	}
	if c.glyphColor != nil {
		fg = c.glyphColor
	}
	return lipgloss.NewStyle().Foreground(fg).Render(ch)
}

// glyphFor returns the card's gutter glyph and the gap that separates
// it from the content it heads.
//
// Cards whose state carries no glyph of its own absorb into the
// timeline instead of leaving a hole in it: the spine character
// becomes the card's left-margin marker — ├─ while a successor exists
// below, ╰─ while the card is the timeline's tail. Those markers are
// two cells wide where a state glyph is one, so the gap narrows by a
// column and content still lands at ContentCol(0).
//
// Spinner frames come in through renderWithGlyph, which supplies its
// own glyph and the standard gap — an animating card always has a
// shape in the gutter, so it can't be glyphless.
func (c *Card) glyphFor(form timelineForm) (glyph, gap string) {
	if _, _, ok := stateGlyph(c.state); ok {
		return c.glyph(), strings.Repeat(" ", GlyphGap)
	}
	shape := BoxCornerBL + BoxHorizontal
	if form == formContinuing {
		shape = BoxTee + BoxHorizontal
	}
	fg := Palette.Recessed
	if c.glyphColor != nil {
		fg = c.glyphColor
	}
	return lipgloss.NewStyle().Foreground(fg).Render(shape), strings.Repeat(" ", GlyphGap-1)
}

// bodyPrefix returns the left-gutter prefix for this card's
// continuation lines: the spine in continuing form, blank columns in
// open form. Both are ContentCol(0)-1 columns wide, so the content
// margin is identical either way and a form swap can never change a
// card's height.
func (c *Card) bodyPrefix(form timelineForm) string {
	if form == formOpen {
		return strings.Repeat(" ", ContentCol(0)-1)
	}
	return " " + c.renderConnector() + "  "
}

// renderConnector returns the styled left-gutter connector for this
// card's continuation lines. Defaults to the recessed timeline gray
// so most cards' body lines visually recede behind their glyph row.
// Cards that opt in via AccentBody() use Palette.Accent instead —
// used by Dialog and similar single-input prompts where the whole
// card-plus-form reads as one continuous active section.
func (c *Card) renderConnector() string {
	color := Palette.Recessed
	if c.accentBody {
		color = Palette.Accent
	}
	return lipgloss.NewStyle().Foreground(color).Render(cardConnector)
}

// maxKVKeyWidth returns the widest KV key across all cardBodyKV
// entries in body. Used to share dot-column alignment across multiple
// KV blocks separated by Text spacers — without this, each KV call
// computes its own width independently and dots land at different
// columns across visual sections.
func maxKVKeyWidth(body []cardBody) int {
	max := 0
	for _, b := range body {
		if b.kind != cardBodyKV {
			continue
		}
		for _, p := range b.pairs {
			if len(p[0]) > max {
				max = len(p[0])
			}
		}
	}
	return max
}

func renderCardBody(b cardBody, kvKeyWidth int) []string {
	normalStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
	mutedStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	errorStyle := lipgloss.NewStyle().Foreground(Palette.Error)

	switch b.kind {
	case cardBodyText:
		out := make([]string, len(b.lines))
		for i, l := range b.lines {
			out[i] = normalStyle.Render(l)
		}
		return out
	case cardBodyMuted, cardBodyStdout:
		out := make([]string, len(b.lines))
		for i, l := range b.lines {
			out[i] = mutedStyle.Render(l)
		}
		return out
	case cardBodyStderr:
		out := make([]string, len(b.lines))
		for i, l := range b.lines {
			out[i] = errorStyle.Render(l)
		}
		return out
	case cardBodyRaw:
		return b.lines
	case cardBodyItem:
		// Item rows are level-1 content. The body connector prefix
		// (" │  ") already advances to ContentCol(0); this lead then
		// carries the glyph to GlyphCol(1) and the gap puts content at
		// ContentCol(1) — the shared L1 grid (see layout.go).
		lead := strings.Repeat(" ", GlyphCol(1)-ContentCol(0)) // 6-5 = 1
		gap := strings.Repeat(" ", GlyphGap)
		out := make([]string, len(b.items))
		for i, item := range b.items {
			out[i] = lead + item.glyph + gap + item.content
		}
		return out
	case cardBodyTable:
		// Compute max visible width per column across all rows.
		// lipgloss.Width is ANSI-aware so styled cells measure
		// by their displayed width, not byte count.
		ncols := 0
		for _, row := range b.table {
			if len(row) > ncols {
				ncols = len(row)
			}
		}
		widths := make([]int, ncols)
		for _, row := range b.table {
			for i, cell := range row {
				if w := lipgloss.Width(cell); w > widths[i] {
					widths[i] = w
				}
			}
		}
		out := make([]string, len(b.table))
		for i, row := range b.table {
			parts := make([]string, ncols)
			for j := 0; j < ncols; j++ {
				cell := ""
				if j < len(row) {
					cell = row[j]
				}
				pad := widths[j] - lipgloss.Width(cell)
				if pad < 0 {
					pad = 0
				}
				parts[j] = cell + strings.Repeat(" ", pad)
			}
			out[i] = strings.Join(parts, "  ")
		}
		return out
	case cardBodyKV:
		// Use the shared width when it's wider than this block's own
		// max so dots line up across KV blocks separated by spacers.
		// Falls back to local max when no shared width was computed
		// (kvKeyWidth == 0).
		maxKey := kvKeyWidth
		for _, p := range b.pairs {
			if len(p[0]) > maxKey {
				maxKey = len(p[0])
			}
		}
		// Prefix width: padded key + " · " (dot with spaces), minus one
		// because the continuation format "% *s %s" adds its own space.
		prefixWidth := maxKey + 2
		// Long values wrap HERE, to the width left of the value column,
		// so every fragment keeps the hanging indent. Left to the
		// generic body pass, wrapForTimeline would re-break overlong
		// lines at the content margin, losing the column. Lines emitted
		// at this width always fit, so that pass leaves them alone —
		// the degraded narrow case below keeps that invariant true
		// rather than assuming it.
		maxContent := TermWidth() - timelineConnWidth
		if maxContent < 20 {
			maxContent = 20
		}
		valueWidth := maxContent - prefixWidth - 1
		contIndent := prefixWidth
		keyOwnLine := false
		if valueWidth < 20 {
			// The full hanging indent leaves no readable value column
			// (very wide key or very narrow terminal). Degrade: the
			// key takes its own line and value fragments wrap at a
			// reduced indent that still guarantees 20 columns —
			// clamping the width alone emitted lines wider than the
			// timeline, which the generic pass then re-broke at the
			// content margin, losing the column entirely.
			valueWidth = 20
			contIndent = maxContent - 21
			if contIndent < 0 {
				contIndent = 0
			}
			keyOwnLine = true
		}
		var out []string
		for _, p := range b.pairs {
			paddedKey := fmt.Sprintf("%-*s", maxKey, p[0])
			first := true
			if keyOwnLine {
				out = append(out, mutedStyle.Render(p[0])+" "+mutedStyle.Render(Palette.Dot))
				first = false
			}
			// Every value line renders in the normal style — a
			// multi-line value is all equally content. Meta lines
			// (Excerpt's "… +K lines" marker) arrive pre-styled muted
			// by their producer; normalStyle wrapping leaves embedded
			// styling in charge (same convention as init's pre-styled
			// "(none)" secret placeholders).
			for _, logical := range strings.Split(p[1], "\n") {
				frags := []string{logical}
				if lipgloss.Width(logical) > valueWidth {
					frags = strings.Split(lipgloss.Wrap(logical, valueWidth, " ,.-"), "\n")
				}
				for _, frag := range frags {
					if first {
						out = append(out, fmt.Sprintf("%s %s %s",
							mutedStyle.Render(paddedKey),
							mutedStyle.Render(Palette.Dot),
							normalStyle.Render(frag),
						))
						first = false
						continue
					}
					out = append(out, fmt.Sprintf("%*s %s",
						contIndent, "",
						normalStyle.Render(frag),
					))
				}
			}
		}
		return out
	}
	return nil
}

// timelineConnWidth is the visual width of the connector prefix
// (" │  ") used for continuation lines beneath a card title.
const timelineConnWidth = 5

// wrapForTimeline word-wraps a string to fit within the terminal width,
// accounting for the timeline connector prefix. Returns the wrapped
// lines. Short strings that fit are returned as-is (single-element slice).
func wrapForTimeline(s string) []string {
	if s == "" {
		return []string{""}
	}
	maxWidth := TermWidth() - timelineConnWidth
	if maxWidth < 20 {
		maxWidth = 20
	}
	// Split embedded newlines first: a short multi-line value (an
	// Excerpt-bounded record, a textarea body) measured whole would
	// come back as ONE element, and the render loop prefixes only the
	// first physical line of each element with the timeline connector
	// — lines 2+ would print flush at column 0.
	var out []string
	for _, part := range strings.Split(s, "\n") {
		if lipgloss.Width(part) <= maxWidth {
			out = append(out, part)
			continue
		}
		out = append(out, strings.Split(lipgloss.Wrap(part, maxWidth, " ,.-"), "\n")...)
	}
	return out
}

// --- Running card with animated spinner glyph ---

type cardSpinnerModel struct {
	spinner     spinner.Model
	card        *Card
	done        bool
	err         error
	resultCh    <-chan error
	successCard func() *Card // optional: if set, render this instead of card on success
	prefix      string       // rendered before the first frame (e.g., comfy connector)
}

func newCardSpinnerModel(card *Card, resultCh <-chan error) cardSpinnerModel {
	s := spinner.New(
		// MiniDot frames are single-column braille chars without
		// trailing padding, keeping alignment with static glyphs.
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return cardSpinnerModel{spinner: s, card: card, resultCh: resultCh}
}

func (m cardSpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForResult())
}

func (m cardSpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case taskDoneMsg:
		m.done = true
		m.err = msg.err
		// Set the card's final state so View() renders the finalized
		// card as BubbleTea's last frame, avoiding a clear-then-reprint
		// flash.
		if m.err != nil {
			m.card.state = CardFailed
			m.card.Subtitle(m.err.Error())
		} else {
			m.card.state = CardSuccess
		}
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.done = true
			m.err = fmt.Errorf("interrupted")
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m cardSpinnerModel) View() tea.View {
	if m.done {
		if m.err == nil && m.successCard != nil {
			return tea.NewView(m.prefix + m.successCard().Render())
		}
		return tea.NewView(m.prefix + m.card.Render())
	}
	return tea.NewView(m.prefix + m.card.renderWithGlyph(m.spinner.View()))
}

func (m cardSpinnerModel) waitForResult() tea.Cmd {
	return func() tea.Msg {
		return taskDoneMsg{err: <-m.resultCh}
	}
}

// RunCard creates a card with the given title, displays it with an
// animated spinner in the glyph position while fn runs, and prints
// the finalized card in success or failed state when fn returns.
func RunCard(title string, fn func() error) error {
	return runCardWithFinalizer(NewCard(CardRunning, title), fn, nil)
}

// RunCardThen is like RunCard but on success replaces the running
// card with whatever successCard() returns (typically the fully
// resolved card with its body content). Useful for per-section
// loading where a card shows just a title with spinner during the
// fetch, then "settles" into the rich resolved card on completion.
//
// The running card preserves the title's case (skips the default
// title-case transform) since its typical use is identifier-titled
// cards (repo names, issue IDs, hostnames) where the input casing
// is meaningful.
//
// On failure, behaves like RunCard — the running card is finalized
// with the failure glyph and the error becomes its subtitle.
//
// successCard MUST be pure with respect to repeated calls: it is
// evaluated once to paint the final frame and once more to record
// what the timeline is showing. See finalFrameCard.
func RunCardThen(title string, fn func() error, successCard func() *Card) error {
	return runCardWithFinalizer(NewCard(CardRunning, title).PreserveCase(), fn, successCard)
}

// runCardWithFinalizer is the inner implementation of RunCard that
// takes a pre-built card so callers can configure indent / tight
// before the spinner runs. The card's state is mutated to its final
// value before printing. On success, a non-nil successCard replaces
// the running card as the final frame (see RunCardThen).
func runCardWithFinalizer(card *Card, fn func() error, successCard func() *Card) error {
	if IsRaw() {
		return fn()
	}
	if s := sessionActive(); s != nil {
		_, err := sessionRunCard(s, card, fn, successCard)
		return err
	}

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	// Capture the comfy connector so BubbleTea renders it atomically
	// with the first spinner frame — no visible gap between the
	// connector and the spinner appearing.
	model := newCardSpinnerModel(card, resultCh)
	model.prefix = spacerPrefix()
	model.successCard = successCard
	p := tea.NewProgram(model)
	result, err := p.Run()
	if err != nil {
		// Non-interactive fallback — wait for the task and print final card.
		taskErr := <-resultCh
		if taskErr != nil {
			card.state = CardFailed
			card.Subtitle(taskErr.Error())
			card.Print()
		} else if successCard != nil {
			successCard().Print()
		} else {
			card.state = CardSuccess
			card.Print()
		}
		return taskErr
	}

	// BubbleTea's final View() already rendered the finalized card
	// in place (state set in Update's taskDoneMsg handler), so the
	// output is on screen. No reprint needed — just record what landed
	// as the timeline's open card and return.
	m := result.(cardSpinnerModel)
	recordCard(finalFrameCard(card, successCard, m.err))
	return m.err
}

// finalFrameCard answers "which card did the spinner program leave on
// screen?" — the mirror of cardSpinnerModel.View's done branch, so the
// card the timeline records is the card the terminal is showing.
//
// This is a SECOND call to successCard: View already made the first
// to paint the frame. successCard must therefore be pure with respect
// to repeated calls (the same contract RunCardSteps documents for its
// successor) — one that rendered differently between the two would
// have the timeline rewrite a block that doesn't match what's on
// screen.
func finalFrameCard(card *Card, successCard func() *Card, err error) *Card {
	if err == nil && successCard != nil {
		return successCard()
	}
	return card
}

// RunCardReplace works like RunCard but on success, prints a replacement
// card (returned by successCard) instead of the original card in success
// state. On failure, prints the original card in failed state as usual.
//
// successCard MUST be pure with respect to repeated calls: it is
// evaluated once to paint the final frame and once more to record
// what the timeline is showing. See finalFrameCard.
func RunCardReplace(title string, fn func() error, successCard func() *Card) error {
	if IsRaw() {
		return fn()
	}

	card := NewCard(CardRunning, title)

	if s := sessionActive(); s != nil {
		_, err := sessionRunCard(s, card, fn, successCard)
		return err
	}

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	sm := newCardSpinnerModel(card, resultCh)
	sm.prefix = spacerPrefix()
	sm.successCard = successCard
	p := tea.NewProgram(sm)
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback.
		taskErr := <-resultCh
		if taskErr != nil {
			card.state = CardFailed
			card.Subtitle(taskErr.Error())
			card.Print()
		} else {
			successCard().Print()
		}
		return taskErr
	}

	// BubbleTea's final View() already rendered the finalized card
	// (or replacement card on success) in place.
	m := model.(cardSpinnerModel)
	recordCard(finalFrameCard(card, successCard, m.err))
	return m.err
}

// RunCardRewindable works like RunCard but on success, prints the
// final card via PrintRewindable and returns the rewind function. The
// rewind erases both the comfy prefix emitted before the spinner and
// the success card, restoring the terminal to its pre-call state.
// On failure the card is printed normally and a nil rewind is returned.
func RunCardRewindable(title string, fn func() error) (func(), error) {
	return RunPreparedCardRewindable(NewCard(CardRunning, title), fn)
}

// RunPreparedCardRewindable is RunCardRewindable taking a pre-built
// card. The caller constructs the card with whatever Value /
// Subtitle / AlignWidth they want visible on the spinner; the
// state is reset to CardRunning during the call and transitions
// to CardSuccess (or CardFailed on error) when fn returns. Use
// when the per-call name is an identifier that must not be
// title-cased — title-cased identifiers like repo names get
// mangled, but Value preserves casing.
func RunPreparedCardRewindable(card *Card, fn func() error) (func(), error) {
	if IsRaw() {
		err := fn()
		return func() {}, err
	}
	if s := sessionActive(); s != nil {
		rec, err := sessionRunCard(s, card, fn, nil)
		if err != nil {
			return nil, err
		}
		return s.sessionRewind(rec), nil
	}

	card.state = CardRunning

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	prevSpacer := needsSpacer
	prefix := spacerPrefix()

	sm := newCardSpinnerModel(card, resultCh)
	sm.prefix = prefix
	p := tea.NewProgram(sm)
	model, err := p.Run()

	// Determine the task result regardless of how BubbleTea exited.
	var taskErr error
	if err != nil {
		taskErr = <-resultCh
	} else {
		taskErr = model.(cardSpinnerModel).err
	}

	if taskErr != nil {
		// BubbleTea's final View() already rendered the failed card.
		recordCard(card)
		return nil, taskErr
	}

	// BubbleTea's final View() rendered the success card in place.
	// Compute total lines (prefix printed before BubbleTea + the
	// card BubbleTea rendered) so the rewind function can erase it.
	rendered := card.Render()
	totalLines := strings.Count(prefix+rendered, "\n")
	rec := recordCard(card)
	return func() {
		if totalLines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", totalLines)
		}
		needsSpacer = prevSpacer
		discardRecord(rec)
	}, nil
}

// titleCase capitalizes the first letter of each word while
// preserving words that already contain uppercase letters (e.g.
// acronyms like "UI" or "API"). Only fully-lowercase words get
// their first rune uppercased.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if w == strings.ToLower(w) {
			r, size := utf8.DecodeRuneInString(w)
			if r != utf8.RuneError {
				words[i] = string(unicode.ToUpper(r)) + w[size:]
			}
		}
	}
	return strings.Join(words, " ")
}
