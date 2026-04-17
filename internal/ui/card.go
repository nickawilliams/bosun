package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"unicode"
	"unicode/utf8"
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

// Card represents a single unit of output in the bosun timeline.
// Cards render with a state glyph in the left gutter and one or more
// content slots (title, subtitle, body) to the right. Continuation
// lines are drawn with a muted connector so a run of cards reads as
// a single vertical timeline.
type Card struct {
	state    CardState
	title    string
	subtitle string
	body     []cardBody
	tight    bool // suppress comfy spacing (e.g. single-field prompts)
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
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	subtitleStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	const pad = " "
	conn := pad + c.renderConnector() + "  "

	// Between the glyph and the title is normally two spaces. Root
	// cards draw a horizontal rule through the title so the heading
	// reads as a visual divider: ╭── Title ─────────────────
	gap := "  "
	if c.state == CardRoot {
		ruleStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
		rendered := renderBreadcrumbTitle(c.title)
		// Title visible width (without ANSI).
		titleWidth := lipgloss.Width(rendered)
		// Available width: terminal minus pad(1) + glyph(1) + pre-dash(1) + space(1) + title + space(1).
		trailLen := TermWidth() - 5 - titleWidth
		if trailLen < 2 {
			trailLen = 2
		}
		trail := ""
		for range trailLen {
			trail += "─"
		}
		gap = ruleStyle.Render("─") + " "
		fmt.Fprintf(&b, "%s%s%s%s %s\n", pad, glyph, gap, rendered, ruleStyle.Render(trail))
	} else {
		fmt.Fprintf(&b, "%s%s%s%s\n", pad, glyph, gap, titleStyle.Render(c.title))
	}

	if c.subtitle != "" {
		fmt.Fprintf(&b, "%s%s\n", conn, subtitleStyle.Render(c.subtitle))
	}

	for _, body := range c.body {
		for _, line := range renderCardBody(body) {
			fmt.Fprintf(&b, "%s%s\n", conn, line)
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
		out := make([]string, len(b.pairs))
		for i, p := range b.pairs {
			paddedKey := fmt.Sprintf("%-*s", maxKey, p[0])
			out[i] = fmt.Sprintf("%s %s %s",
				mutedStyle.Render(paddedKey),
				mutedStyle.Render(Palette.Dot),
				normalStyle.Render(p[1]),
			)
		}
		return out
	}
	return nil
}

// --- Running card with animated spinner glyph ---

type cardSpinnerModel struct {
	spinner  spinner.Model
	card     *Card
	done     bool
	err      error
	resultCh <-chan error
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
		return tea.NewView("")
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
	card := NewCard(CardRunning, title)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- fn()
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
		} else {
			card.state = CardSuccess
		}
		card.Print()
		return taskErr
	}

	m := model.(cardSpinnerModel)
	if m.err != nil {
		card.state = CardFailed
		card.Print()
		return m.err
	}
	card.state = CardSuccess
	card.Print()
	return nil
}

// RunCardReplace works like RunCard but on success, prints a replacement
// card (returned by successCard) instead of the original card in success
// state. On failure, prints the original card in failed state as usual.
func RunCardReplace(title string, fn func() error, successCard func() *Card) error {
	card := NewCard(CardRunning, title)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- fn()
	}()

	fmt.Print(comfyPrefix())

	p := tea.NewProgram(newCardSpinnerModel(card, resultCh))
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback.
		taskErr := <-resultCh
		if taskErr != nil {
			card.state = CardFailed
			card.Print()
		} else {
			successCard().Print()
		}
		return taskErr
	}

	m := model.(cardSpinnerModel)
	if m.err != nil {
		card.state = CardFailed
		card.Print()
		return m.err
	}
	successCard().Print()
	return nil
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
