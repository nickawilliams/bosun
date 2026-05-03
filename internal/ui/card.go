package ui

import (
	_ "embed"
	"fmt"
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
//   - CardSuccess — operation completed successfully
//   - CardFailed  — operation was attempted and returned an error
//   - CardSkipped — operation was not attempted (missing config, optional
//     dependency unavailable, precondition unmet)
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
	CardData // structured state snapshot, no status glyph
)

const (
	cardConnector    = "│"
	cardGlyphPending = "◦"
	cardGlyphSuccess = "✓"
	cardGlyphSkipped = "!"
	cardGlyphFailed  = "✗"
	cardGlyphInfo    = "●"
	cardGlyphInput   = "?"
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
	state    CardState
	title    string
	value    string // Rendered after title as-is (no title-casing), muted style.
	subtitle string
	body     []cardBody
	tight    bool // suppress comfy spacing (e.g. single-field prompts)
	indent   int  // additional left-margin depth (1 = +4 spaces); used by Group children
}

type cardBodyKind int

const (
	cardBodyText cardBodyKind = iota
	cardBodyMuted
	cardBodyKV
	cardBodyStdout
	cardBodyStderr
	cardBodyRaw // pre-styled lines, no additional formatting
)

type cardBody struct {
	kind  cardBodyKind
	lines []string
	pairs [][2]string // used by cardBodyKV
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

// Print writes the card to stdout.
func (c *Card) Print() {
	fmt.Print(comfyPrefix() + c.Render())
	if !c.tight {
		comfyBreak = true
	}
}

// PrintRewindable writes the card to stdout and returns a function
// that, when called, erases the card by moving the cursor back to
// its first row and clearing from there to the end of the screen.
// Useful for transient "live" cards (e.g., an active prompt) that
// should be replaced with a terminal-state card after an interactive
// operation completes. Note: this only works reliably when huh/
// BubbleTea cleans up its own output on normal exit. For ctrl+c
// interrupts, callers should skip the rewind and append output
// below the interrupted form instead.
func (c *Card) PrintRewindable() func() {
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
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary).Transform(titleCase)
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
		segments := strings.Split(c.title, " › ")
		if len(segments) > 1 {
			primaryStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
			sepStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Recessed)
			styledSegs := make([]string, len(segments)-1)
			for i, seg := range segments[1:] {
				styledSegs[i] = primaryStyle.Render(titleCase(seg))
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

	for _, body := range c.body {
		for _, line := range renderCardBody(body) {
			for _, wrapped := range wrapForTimeline(line) {
				fmt.Fprintf(&b, "%s%s\n", conn, wrapped)
			}
		}
	}

	return b.String()
}

// glyph returns the styled state glyph for this card.
func (c *Card) glyph() string {
	switch c.state {
	case CardPending:
		return lipgloss.NewStyle().Foreground(Palette.Muted).Render(cardGlyphPending)
	case CardRunning:
		return lipgloss.NewStyle().Foreground(Palette.Primary).Render(cardGlyphPending)
	case CardSuccess:
		return lipgloss.NewStyle().Foreground(Palette.Success).Render(cardGlyphSuccess)
	case CardSkipped:
		return lipgloss.NewStyle().Foreground(Palette.Warning).Render(cardGlyphSkipped)
	case CardFailed:
		return lipgloss.NewStyle().Foreground(Palette.Error).Render(cardGlyphFailed)
	case CardInfo:
		return lipgloss.NewStyle().Foreground(Palette.Primary).Render(cardGlyphInfo)
	case CardInput:
		return lipgloss.NewStyle().Foreground(Palette.Accent).Render(cardGlyphInput)
	case CardRoot:
		return lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardGlyphRoot)
	case CardData:
		return lipgloss.NewStyle().Foreground(Palette.Muted).Render("·")
	}
	return " "
}

// renderConnector returns the styled left-gutter connector for this
// card's continuation lines.
func (c *Card) renderConnector() string {
	return lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
}

func renderCardBody(b cardBody) []string {
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
	case cardBodyKV:
		maxKey := 0
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

// RunCard creates a card with the given title, displays it with an
// animated spinner in the glyph position while fn runs, and prints
// the finalized card in success or failed state when fn returns.
func RunCard(title string, fn func() error) error {
	return runCardWith(NewCard(CardRunning, title), fn)
}

// runCardWith is the inner implementation of RunCard that takes a
// pre-built card so callers can configure indent / tight before the
// spinner runs. The card's state is mutated to its final value
// before printing.
func runCardWith(card *Card, fn func() error) error {
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

	p := tea.NewProgram(newCardSpinnerModel(card, resultCh))
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback — wait for the task and print final card.
		taskErr := <-resultCh
		if taskErr != nil {
			card.state = CardFailed
			card.Subtitle(taskErr.Error())
		} else {
			card.state = CardSuccess
		}
		card.Print()
		return taskErr
	}

	// BubbleTea's final View() already rendered the finalized card
	// in place (state set in Update's taskDoneMsg handler), so the
	// output is on screen. No reprint needed — just propagate the
	// comfy break state and return.
	m := model.(cardSpinnerModel)
	if !card.tight {
		comfyBreak = true
	}
	return m.err
}

// RunCardReplace works like RunCard but on success, prints a replacement
// card (returned by successCard) instead of the original card in success
// state. On failure, prints the original card in failed state as usual.
func RunCardReplace(title string, fn func() error, successCard func() *Card) error {
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

// renderBreadcrumbTitle renders a breadcrumb title (segments joined
// by " › ") with the first segment in Secondary and the rest in
// Primary. Non-breadcrumb titles render entirely in Primary.
func renderBreadcrumbTitle(title string) string {
	primaryStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	secondaryStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Secondary)
	sepStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Recessed)

	segments := strings.Split(title, " › ")
	if len(segments) <= 1 {
		return primaryStyle.Render(titleCase(title))
	}

	styled := make([]string, len(segments))
	for i, seg := range segments {
		tc := titleCase(seg)
		if i == 0 {
			styled[i] = secondaryStyle.Render(tc)
		} else {
			styled[i] = primaryStyle.Render(tc)
		}
	}
	return strings.Join(styled, sepStyle.Render(" › "))
}
