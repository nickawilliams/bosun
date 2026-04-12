package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CardState represents the lifecycle state of a card.
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
}

type cardBodyKind int

const (
	cardBodyText cardBodyKind = iota
	cardBodyMuted
	cardBodyKV
	cardBodyStdout
	cardBodyStderr
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
	fmt.Print(c.Render())
}

// PrintRewindable writes the card to stdout and returns a function
// that, when called, erases the card by moving the cursor back to
// its first row and clearing from there to the end of the screen.
// Useful for transient "live" cards (e.g., an active prompt) that
// should be replaced with a terminal-state card after an interactive
// operation completes.
func (c *Card) PrintRewindable() func() {
	rendered := c.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	return func() {
		if lines > 0 {
			// CPL (cursor previous line) + ED (erase to end of screen).
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
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
	// cards instead draw a short horizontal line + space so the
	// corner glyph extends into a labeled branch (╭─ Title). The
	// total width stays the same, keeping all card titles aligned.
	gap := "  "
	if c.state == CardRoot {
		gap = lipgloss.NewStyle().Foreground(cardConnectorColor).Render("─") + " "
	}

	fmt.Fprintf(&b, "%s%s%s%s\n", pad, glyph, gap, titleStyle.Render(c.title))

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
		return lipgloss.NewStyle().Foreground(cardConnectorColor).Render(cardGlyphRoot)
	}
	return " "
}

// cardConnectorColor is deliberately darker than Palette.Muted so the
// vertical timeline spine recedes behind the content it ties together.
var cardConnectorColor = lipgloss.Color("237")

// renderConnector returns the styled left-gutter connector for this
// card's continuation lines. All cards (including CardInput) use
// the recessed gray spine — the fuchsia accent is reserved for the
// single row receiving input.
func (c *Card) renderConnector() string {
	return lipgloss.NewStyle().Foreground(cardConnectorColor).Render(cardConnector)
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
