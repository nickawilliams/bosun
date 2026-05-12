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
)

const (
	cardConnector    = "│"
	cardGlyphPending = "◦"
	cardGlyphSuccess = "✓"
	cardGlyphSkipped = "▲" // !▲
	cardGlyphFailed  = "✗"
	cardGlyphInfo    = "●"
	cardGlyphInput   = "?"
	cardGlyphReady   = "●" // *●◆
	cardGlyphWaiting = "⧗" // ~⧗●◆
	// CardRoot uses the top-left rounded corner box-drawing
	// character in the connector color so the root visually
	// anchors the timeline: the corner turns into the vertical
	// spine that runs through every card below.
	cardGlyphRoot = "╭"
)

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
	// absorbedGlyph, when non-empty, is a pre-styled glyph rendered
	// in front of the LAST breadcrumb segment of a CardRoot. Used by
	// the squish path to put a state glyph (or animated spinner
	// frame) before the absorbed segment's title without the
	// breadcrumb's own styling overwriting its color.
	absorbedGlyph         string
	suppressAbsorbedGlyph bool        // when true, breadcrumb absorption renders the most-recent data segment with no leading glyph
	absorbedTitleColor    color.Color // optional override; applied to all dataSegments
	dataSegments          []string    // breadcrumb data segments inserted between the implicit "bosun" root and the command-path tail (option 2 layout)
	chainAbsorption       bool        // when true and absorbed body-less, the squish chain stays armed so the next card also absorbs as another data segment
	tightBody             bool        // when true, renderBodyAndSubtitle skips the leading blank-connector line
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

// NewCard creates a card with the given state and title.
func NewCard(state CardState, title string) *Card {
	return &Card{state: state, title: title}
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

// TightBody suppresses the blank connector line normally inserted
// between the card's breadcrumb / title and the start of its body
// when absorbed via the squish path. Use when the body's first row
// is already a header-style line (so the auto-pad would create
// double spacing) or when vertical density matters more than the
// usual breathing room.
func (c *Card) TightBody() *Card {
	c.tightBody = true
	return c
}

// HideAbsorbedGlyph suppresses the leading state glyph when this
// card is squished into a parent root card's breadcrumb. The
// breadcrumb segment renders only the card's title, with no
// glyph or color marker before it. Use when the absorbed segment
// is purely informational and the state glyph would add noise.
func (c *Card) HideAbsorbedGlyph() *Card {
	c.suppressAbsorbedGlyph = true
	return c
}

// AbsorbedTitleColor overrides the foreground color used for this
// card's title when it absorbs into a parent root card's
// breadcrumb. Default is Palette.Primary; pass a different color
// to mark the absorbed segment as a distinct kind of content
// (e.g., a data identifier rather than a command name).
func (c *Card) AbsorbedTitleColor(col color.Color) *Card {
	c.absorbedTitleColor = col
	return c
}

// ChainAbsorption marks this card as a chain-absorption participant.
// When the card absorbs into a breadcrumb as body-less, the squish
// chain stays armed so the next card also absorbs as another data
// segment. Use to compose multi-segment breadcrumbs (e.g.,
// `bosun › Foo › Bar › Baz`) from a sequence of standalone cards.
//
// Default behavior for a body-less absorbed card is to absorb once
// and stop — so accidentally body-less cards don't silently steal
// the next card's slot. Opt in explicitly when chaining is desired.
func (c *Card) ChainAbsorption() *Card {
	c.chainAbsorption = true
	return c
}

// Breadcrumb appends a data segment to this card's breadcrumb. Use
// when the segment value is known synchronously at card construction
// — the segment renders inline with the title on first paint, no
// absorption / re-render dance required. Compose with
// AbsorbedTitleColor to tint all data segments.
//
// Compare to ChainAbsorption: that mechanism handles segments
// discovered asynchronously (e.g., an issue key resolved after a
// tracker fetch); this method handles segments known up front.
func (c *Card) Breadcrumb(s string) *Card {
	if s == "" {
		return c
	}
	c.dataSegments = append(c.dataSegments, s)
	return c
}

// Value sets an inline value rendered after the title, separated by a colon.
// The value is not title-cased and uses muted non-bold style.
func (c *Card) Value(s string) *Card {
	c.value = s
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

// Render returns the card as a styled multi-line string ending in a newline.
func (c *Card) Render() string {
	return c.renderWithGlyph(c.glyph())
}

// Print writes the card to stdout. Suppressed in raw mode.
//
// Root-card absorption: if a CardRoot was just printed, the next
// non-root Card.Print "squishes" — its title is appended to the
// root's breadcrumb (separated by " › ") and any body content is
// rendered below the (re-rendered) root box. A title-less and
// body-less card consumes the squish slot without modifying the
// root, providing an opt-out by emitting an inert card.
func (c *Card) Print() {
	if IsRaw() {
		return
	}
	if squishPending && c.state != CardRoot {
		squishConsume(c)
		return
	}
	rendered := c.Render()
	fmt.Print(comfyPrefix() + rendered)
	if !c.tight {
		comfyBreak = true
	}
	rememberRootForSquish(c, rendered)
}

// PrintRewindable writes the card to stdout and returns a function
// that, when called, erases the card by moving the cursor back to
// its first row and clearing from there to the end of the screen.
// Suppressed in raw mode (returns a no-op rewind).
func (c *Card) PrintRewindable() func() {
	if IsRaw() {
		return func() {}
	}
	prev := comfyBreak
	rendered := comfyPrefix() + c.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	if !c.tight {
		comfyBreak = true
	}
	return func() {
		if lines > 0 {
			// CPL (cursor previous line) + ED (erase to end of screen).
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
		comfyBreak = prev
	}
}

// renderWithGlyph renders the card with a custom leading glyph.
// Used by the spinner to animate the state indicator in place.
func (c *Card) renderWithGlyph(glyph string) string {
	out := c.renderInner(glyph)
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
		prefix += " " + connStyle.Render("│") + "  "
	}
	trimmed := strings.TrimSuffix(out, "\n")
	lines := strings.Split(trimmed, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderInner is the indent-agnostic render path.
func (c *Card) renderInner(glyph string) string {
	var b strings.Builder
	titleFg := Palette.Primary
	if c.titleColor != nil {
		titleFg = c.titleColor
	}
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(titleFg)
	if !c.preserveTitle {
		titleStyle = titleStyle.Transform(titleCase)
	}
	subtitleStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	const pad = " "
	conn := pad + c.renderConnector() + "  "

	// Between the glyph and the title is normally two spaces. Root
	// cards wrap the ASCII art logo in a full-width box that extends
	// to the terminal edge. The left side doubles as the timeline
	// connector and the right border sweeps back to the breadcrumb:
	//
	//  ╭──────────────────────────────────────────────────────────╮
	//  │  ▗▄▄▖  ▗▄▖  ▗▄▄▖▗▖ ▗▖▗▖  ▗▖                            │
	//  │  ...                                                     │
	//  │                                                          │
	//  │  Command ────────────────────────────────────────────────╯
	//  │
	gap := "  "
	if c.state == CardRoot {
		ruleStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
		logoStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Secondary)

		// Box spans the full terminal width.
		// Layout: pad(1) + border(1) + inner + border(1) = TermWidth()
		boxInner := TermWidth() - 3
		if boxInner < 10 {
			boxInner = 10
		}

		// Top border — the glyph (╭) starts the box.
		fmt.Fprintf(&b, "%s%s%s\n", pad, glyph,
			ruleStyle.Render(strings.Repeat("─", boxInner)+"╮"))

		// Logo lines: │  art ...padding... │
		// The first line includes the version string right-aligned.
		versionStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		versionStr := versionStyle.Render(AppVersion)
		versionWidth := lipgloss.Width(versionStr)
		for i, line := range asciiLogo {
			artWidth := lipgloss.Width(line)
			if i == 0 {
				// │  art  ...padding...  version  │
				rightPad := boxInner - 2 - artWidth - versionWidth - 2
				if rightPad < 1 {
					rightPad = 1
				}
				fmt.Fprintf(&b, "%s%s  %s%s%s  %s\n", pad,
					ruleStyle.Render("│"),
					logoStyle.Render(line),
					strings.Repeat(" ", rightPad),
					versionStr,
					ruleStyle.Render("│"))
			} else {
				rightPad := boxInner - 2 - artWidth
				if rightPad < 1 {
					rightPad = 1
				}
				fmt.Fprintf(&b, "%s%s  %s%s%s\n", pad,
					ruleStyle.Render("│"),
					logoStyle.Render(line),
					strings.Repeat(" ", rightPad),
					ruleStyle.Render("│"))
			}
		}

		// Bottom: breadcrumb closes the right side with ╯.
		// Layout: <data segments> › <command-path tail>. The implicit
		// "bosun" root is dropped (the logo above stands in for it),
		// data segments come next (in green when absorbedTitleColor
		// is set), then the command-path segments.
		titleSegs := strings.Split(c.title, " › ")
		var commandTail []string
		if len(titleSegs) > 1 {
			commandTail = titleSegs[1:]
		}
		visible := append([]string{}, c.dataSegments...)
		visible = append(visible, commandTail...)
		if len(visible) > 0 {
			primaryStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
			sepStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Recessed)
			styledSegs := make([]string, len(visible))
			dataLen := len(c.dataSegments)
			// The absorbed glyph (if any) prefixes the most recently
			// absorbed data segment — i.e., the LAST data segment.
			lastDataIdx := dataLen - 1
			for i, seg := range visible {
				style := primaryStyle
				if i < dataLen && c.absorbedTitleColor != nil {
					style = lipgloss.NewStyle().Bold(true).Foreground(c.absorbedTitleColor)
				}
				styled := style.Render(titleCase(seg))
				if i == lastDataIdx && c.absorbedGlyph != "" {
					styled = c.absorbedGlyph + " " + styled
				}
				styledSegs[i] = styled
			}
			breadcrumb := strings.Join(styledSegs, sepStyle.Render(" › "))

			// Optional prefix/postfix glyphs around the breadcrumb.
			prefix := ""
			prefixWidth := 0
			if BreadcrumbPrefix != "" {
				prefix = ruleStyle.Render(BreadcrumbPrefix) + " "
				prefixWidth = lipgloss.Width(prefix)
			}
			postfix := " "
			postfixWidth := 1
			if BreadcrumbPostfix != "" {
				postfix = " " + ruleStyle.Render(BreadcrumbPostfix) + " "
				postfixWidth = lipgloss.Width(BreadcrumbPostfix) + 2
			}

			ruleLen := boxInner - 2 - prefixWidth - lipgloss.Width(breadcrumb) - postfixWidth
			if ruleLen < 1 {
				ruleLen = 1
			}
			fmt.Fprintf(&b, "%s%s  %s%s%s%s\n", pad,
				ruleStyle.Render("│"),
				prefix,
				breadcrumb,
				postfix,
				ruleStyle.Render(strings.Repeat("─", ruleLen)+"╯"))
		} else {
			fmt.Fprintf(&b, "%s%s  %s\n", pad,
				ruleStyle.Render("│"),
				ruleStyle.Render(strings.Repeat("─", boxInner-2)+"╯"))
		}
	} else {
		if c.value != "" {
			valueStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
			fmt.Fprintf(&b, "%s%s%s%s %s\n", pad, glyph, gap, titleStyle.Render(c.title+":"), valueStyle.Render(c.value))
		} else {
			fmt.Fprintf(&b, "%s%s%s%s\n", pad, glyph, gap, titleStyle.Render(c.title))
		}
	}

	if c.subtitle != "" {
		for _, line := range wrapForTimeline(c.subtitle) {
			fmt.Fprintf(&b, "%s%s\n", conn, subtitleStyle.Render(line))
		}
	}

	// Root cards visually separate the title row from the body content
	// with a blank connector line.
	if c.state == CardRoot && len(c.body) > 0 {
		fmt.Fprintf(&b, "%s\n", conn)
	}

	kvWidth := maxKVKeyWidth(c.body)
	for _, body := range c.body {
		for _, line := range renderCardBody(body, kvWidth) {
			for _, wrapped := range wrapForTimeline(line) {
				fmt.Fprintf(&b, "%s%s\n", conn, wrapped)
			}
		}
	}

	return b.String()
}

// renderBodyAndSubtitle returns just the subtitle + body portion
// of the card (no title row, no glyph). Used by squish-mode
// rendering, which prints the absorbed card's title in the parent
// breadcrumb but still wants its body content below.
//
// Output starts with a blank connector line — the same visual
// separator CardRoot uses between its title row and body — so the
// absorbed body reads as a distinct block under the breadcrumb
// rather than crowding directly against it. The blank line is
// suppressed when the card opts into TightBody().
func (c *Card) renderBodyAndSubtitle() string {
	if c.subtitle == "" && len(c.body) == 0 {
		return ""
	}
	var b strings.Builder
	subtitleStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	const pad = " "
	conn := pad + c.renderConnector() + "  "
	if !c.tightBody {
		fmt.Fprintf(&b, "%s\n", pad+c.renderConnector())
	}
	if c.subtitle != "" {
		for _, line := range wrapForTimeline(c.subtitle) {
			fmt.Fprintf(&b, "%s%s\n", conn, subtitleStyle.Render(line))
		}
	}
	kvWidth := maxKVKeyWidth(c.body)
	for _, body := range c.body {
		for _, line := range renderCardBody(body, kvWidth) {
			for _, wrapped := range wrapForTimeline(line) {
				fmt.Fprintf(&b, "%s%s\n", conn, wrapped)
			}
		}
	}
	return b.String()
}

// glyph returns the styled state glyph for this card. When
// Card.GlyphColor has been set, it overrides the state's natural
// color while preserving the glyph shape.
func (c *Card) glyph() string {
	var ch string
	var fg color.Color
	switch c.state {
	case CardPending:
		ch, fg = cardGlyphPending, Palette.Muted
	case CardRunning:
		ch, fg = cardGlyphPending, Palette.Primary
	case CardSuccess:
		ch, fg = cardGlyphSuccess, Palette.Success
	case CardSkipped:
		ch, fg = cardGlyphSkipped, Palette.Warning
	case CardFailed:
		ch, fg = cardGlyphFailed, Palette.Error
	case CardInfo:
		ch, fg = cardGlyphInfo, Palette.Primary
	case CardInput:
		ch, fg = cardGlyphInput, Palette.Accent
	case CardRoot:
		ch, fg = cardGlyphRoot, Palette.Recessed
	case CardData:
		ch, fg = cardGlyphInfo, Palette.Primary
	case CardReady:
		ch, fg = cardGlyphReady, Palette.Success
	case CardWaiting:
		ch, fg = cardGlyphWaiting, Palette.Info
	default:
		return " "
	}
	if c.glyphColor != nil {
		fg = c.glyphColor
	}
	return lipgloss.NewStyle().Foreground(fg).Render(ch)
}

// renderConnector returns the styled left-gutter connector for this
// card's continuation lines.
func (c *Card) renderConnector() string {
	return lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
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
		// One leading space so the glyph lands at col 6, aligning
		// with Group child glyphs (which sit one column right of
		// the parent's │ + 2-space gap).
		out := make([]string, len(b.items))
		for i, item := range b.items {
			out[i] = " " + item.glyph + "  " + item.content
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
		var out []string
		for _, p := range b.pairs {
			lines := strings.Split(p[1], "\n")
			paddedKey := fmt.Sprintf("%-*s", maxKey, p[0])
			out = append(out, fmt.Sprintf("%s %s %s",
				mutedStyle.Render(paddedKey),
				mutedStyle.Render(Palette.Dot),
				normalStyle.Render(lines[0]),
			))
			// Continuation lines aligned under the value column.
			for _, cont := range lines[1:] {
				out = append(out, fmt.Sprintf("%*s %s",
					prefixWidth, "",
					mutedStyle.Render(cont),
				))
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
	if lipgloss.Width(s) <= maxWidth {
		return []string{s}
	}
	wrapped := lipgloss.Wrap(s, maxWidth, " ,.-")
	return strings.Split(wrapped, "\n")
}

// --- Running card with animated spinner glyph ---

type cardSpinnerModel struct {
	spinner     spinner.Model
	card        *Card
	done        bool
	err         error
	resultCh    <-chan error
	successCard func() *Card // optional: if set, render this instead of card on success
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
		// Render the finalized card so BubbleTea's last frame
		// replaces the spinner in place without a visible gap.
		if m.err == nil && m.successCard != nil {
			return tea.NewView(m.successCard().Render())
		}
		return tea.NewView(m.card.Render())
	}
	return tea.NewView(m.card.renderWithGlyph(m.spinner.View()))
}

func (m cardSpinnerModel) waitForResult() tea.Cmd {
	return func() tea.Msg {
		return taskDoneMsg{err: <-m.resultCh}
	}
}

// squishedSpinnerModel renders a previously-printed root card with
// an extended breadcrumb whose final segment is the running task's
// title, prefixed by an animated spinner. On result, the spinner
// frame is replaced with a success or failure glyph; if a success
// card builder is set, its title + glyph replace the running
// title and its subtitle/body render below the root box.
type squishedSpinnerModel struct {
	spinner     spinner.Model
	root        *Card        // copy of the original root card (title is its base breadcrumb)
	title       string       // running task title appended as the final breadcrumb segment
	successCard func() *Card // optional: builds the replacement card on success
	done        bool
	err         error
	resultCh    <-chan error
}

func newSquishedSpinnerModel(root *Card, title string, successCard func() *Card, resultCh <-chan error) squishedSpinnerModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return squishedSpinnerModel{
		spinner:     s,
		root:        root,
		title:       title,
		successCard: successCard,
		resultCh:    resultCh,
	}
}

func (m squishedSpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForResult())
}

func (m squishedSpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case taskDoneMsg:
		m.done = true
		m.err = msg.err
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

func (m squishedSpinnerModel) View() tea.View {
	if m.done {
		// Final frame: same breadcrumb assembly used by the
		// non-interactive fallback so the spinner→final transition
		// stays a constant line count. Body content prints after
		// the program exits — see runSquishedCard.
		extended, _ := squishedFinalExtended(m.root, m.title, m.successCard, m.err)
		return tea.NewView(extended.Render())
	}
	extended := *m.root
	extended.dataSegments = append(append([]string{}, m.root.dataSegments...), m.title)
	extended.absorbedGlyph = m.spinner.View()
	return tea.NewView(extended.Render())
}

func (m squishedSpinnerModel) waitForResult() tea.Cmd {
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
func RunCardThen(title string, fn func() error, successCard func() *Card) error {
	return runCardWithFinalizer(NewCard(CardRunning, title).PreserveCase(), fn, successCard)
}

// runSquishedCard is the squish-mode counterpart to runCardWith.
// It runs fn while animating a spinner glyph in the LAST segment
// of the previously-printed root card's breadcrumb (with the
// passed card's title appended). On success, if successCard is
// non-nil, its title and glyph replace the running title in the
// breadcrumb (and its subtitle/body render below). On failure the
// spinner frame is replaced with the failed glyph and the running
// title stays in the breadcrumb.
//
// successCard may be nil — in that case the running title remains
// in the breadcrumb with a ✓ glyph on success.
func runSquishedCard(card *Card, fn func() error, successCard func() *Card) error {
	if IsRaw() {
		return fn()
	}

	root := squishCurrent.root
	rootLines := squishCurrent.lineCount
	clearSquish()

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	// Erase the previously-printed root card; bubbletea will paint
	// the extended version starting at the same screen position.
	if rootLines > 0 {
		fmt.Printf("\x1b[%dF\x1b[J", rootLines)
	}

	p := tea.NewProgram(newSquishedSpinnerModel(root, card.title, successCard, resultCh))
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback — wait for the task and print a
		// finalized extended root + body.
		taskErr := <-resultCh
		extended, body := squishedFinalExtended(root, card.title, successCard, taskErr)
		fmt.Print(extended.Render() + body)
		comfyBreak = true
		return taskErr
	}

	// Bubbletea rendered the extended root with the resolved
	// breadcrumb in place; now print the body content separately
	// so its line count doesn't have to fit inside the program's
	// constant-line-count frame.
	m := model.(squishedSpinnerModel)
	_, body := squishedFinalExtended(root, card.title, successCard, m.err)
	if body != "" {
		fmt.Print(body)
	}
	comfyBreak = true
	return m.err
}

// squishedFinalRender produces the static "after-task" render of a
// squished root card — used by the bubbletea final frame and by
// the non-interactive fallback path. On success with a successCard
// builder, the breadcrumb shows the replacement's title + glyph
// and the replacement's subtitle/body render below the box.
// squishedFinalExtended builds the post-task extended root card
// (breadcrumb only — no body). The body is printed separately by
// the caller after the bubbletea program exits, so the program's
// per-frame line count stays constant during the spinner→final
// transition.
func squishedFinalExtended(root *Card, runningTitle string, successCard func() *Card, err error) (extended Card, body string) {
	extended = *root
	base := append([]string{}, root.dataSegments...)
	if err == nil && successCard != nil {
		repl := successCard()
		// Empty title = "no new breadcrumb segment, just render the
		// body below." Used for transient loading patterns where the
		// spinner replaces with body content (no further identity).
		if repl.title != "" {
			extended.dataSegments = append(base, repl.title)
			if !repl.suppressAbsorbedGlyph {
				extended.absorbedGlyph = repl.glyph()
			} else {
				extended.absorbedGlyph = ""
			}
		} else {
			extended.absorbedGlyph = ""
		}
		if repl.absorbedTitleColor != nil {
			extended.absorbedTitleColor = repl.absorbedTitleColor
		}
		body = repl.renderBodyAndSubtitle()
	} else {
		extended.dataSegments = append(base, runningTitle)
		extended.absorbedGlyph = squishedFinalGlyph(err)
	}
	return extended, body
}

// squishedFinalGlyph picks the styled glyph for a completed
// squish-mode RunCard: ✓ on success, ✗ on failure.
func squishedFinalGlyph(err error) string {
	if err != nil {
		return lipgloss.NewStyle().Foreground(Palette.Error).Render(cardGlyphFailed)
	}
	return lipgloss.NewStyle().Foreground(Palette.Success).Render(cardGlyphSuccess)
}

// runCardWith is the inner implementation of RunCard that takes a
// pre-built card so callers can configure indent / tight before the
// spinner runs. The card's state is mutated to its final value
// before printing.
func runCardWithFinalizer(card *Card, fn func() error, successCard func() *Card) error {
	// Raw mode: run synchronously without BubbleTea to avoid
	// terminal-mode-query escape leaks on non-TTY stdout.
	if IsRaw() {
		return fn()
	}
	if squishPending {
		return runSquishedCard(card, fn, successCard)
	}

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	// Emit comfy connector before the spinner starts — BubbleTea's
	// View() bypasses Print() so it won't consume the prefix itself.
	fmt.Print(comfyPrefix())

	model := newCardSpinnerModel(card, resultCh)
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
	// output is on screen. No reprint needed — just propagate the
	// comfy break state and return.
	m := result.(cardSpinnerModel)
	if !card.tight {
		comfyBreak = true
	}
	return m.err
}

// RunCardReplace works like RunCard but on success, prints a replacement
// card (returned by successCard) instead of the original card in success
// state. On failure, prints the original card in failed state as usual.
func RunCardReplace(title string, fn func() error, successCard func() *Card) error {
	if IsRaw() {
		return fn()
	}
	if squishPending {
		// Squish-mode honors the replacement: on success, the
		// successCard's title + glyph take the breadcrumb's last
		// segment, and its subtitle/body render below the root box.
		return runSquishedCard(NewCard(CardRunning, title), fn, successCard)
	}

	card := NewCard(CardRunning, title)

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	fmt.Print(comfyPrefix())

	sm := newCardSpinnerModel(card, resultCh)
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
	comfyBreak = true
	return m.err
}

// RunCardRewindable works like RunCard but on success, prints the
// final card via PrintRewindable and returns the rewind function. The
// rewind erases both the comfy prefix emitted before the spinner and
// the success card, restoring the terminal to its pre-call state.
// On failure the card is printed normally and a nil rewind is returned.
func RunCardRewindable(title string, fn func() error) (func(), error) {
	if IsRaw() {
		err := fn()
		return func() {}, err
	}
	if squishPending {
		// Squish-mode: the action lives in the root breadcrumb
		// permanently — there's nothing to "rewind" to a prior
		// state. Return a no-op rewind so callers using this for
		// transient overlay rendering degrade gracefully.
		err := runSquishedCard(NewCard(CardRunning, title), fn, nil)
		return func() {}, err
	}

	card := NewCard(CardRunning, title)

	resultCh := make(chan error, 1)
	go func() {
		start := time.Now()
		err := fn()
		holdSpinner(start)
		resultCh <- err
	}()

	prevComfy := comfyBreak
	prefix := comfyPrefix()
	fmt.Print(prefix)

	p := tea.NewProgram(newCardSpinnerModel(card, resultCh))
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
		return nil, taskErr
	}

	// BubbleTea's final View() rendered the success card in place.
	// Compute total lines (prefix printed before BubbleTea + the
	// card BubbleTea rendered) so the rewind function can erase it.
	rendered := card.Render()
	totalLines := strings.Count(prefix+rendered, "\n")
	if !card.tight {
		comfyBreak = true
	}
	return func() {
		if totalLines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", totalLines)
		}
		comfyBreak = prevComfy
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
