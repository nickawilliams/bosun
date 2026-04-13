package ui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PlanCardState represents the lifecycle phase of a plan card.
type PlanCardState int

const (
	PlanProposed  PlanCardState = iota // ? glyph — awaiting confirmation
	PlanApplying                       // spinner — executing actions
	PlanSuccess                        // ✓ — all actions succeeded
	PlanPartial                        // ! — some actions failed
	PlanFailure                        // ✗ — all/critical actions failed
	PlanCancelled                      // ! — user declined
)

// PlanCard is a stateful plan component that transitions through lifecycle
// states, updating in place. It owns the visual representation of the entire
// plan from proposal through execution.
type PlanCard struct {
	plan      *Plan
	state     PlanCardState
	succeeded int
	failed    int
}

// NewPlanCard creates a plan card in the Proposed state.
func NewPlanCard(plan *Plan) *PlanCard {
	return &PlanCard{plan: plan, state: PlanProposed}
}

// SetState transitions the card to a new state.
func (pc *PlanCard) SetState(s PlanCardState) {
	pc.state = s
}

// SetResults records how many actions succeeded/failed (for partial/success).
func (pc *PlanCard) SetResults(succeeded, failed int) {
	pc.succeeded = succeeded
	pc.failed = failed
}

// Render returns the card as a styled string.
func (pc *PlanCard) Render() string {
	return pc.renderWithGlyph(pc.glyph())
}

// Print writes the card to stdout.
func (pc *PlanCard) Print() {
	fmt.Print(pc.Render())
}

// PrintRewindable writes the card to stdout and returns a function that
// erases it via ANSI cursor movement.
func (pc *PlanCard) PrintRewindable() func() {
	rendered := pc.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	return func() {
		if lines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
	}
}

// renderWithGlyph renders the card with a custom glyph string (used by
// the BubbleTea spinner model to inject animated frames).
func (pc *PlanCard) renderWithGlyph(glyph string) string {
	var b strings.Builder

	headingStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	connStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)

	// Title line: glyph + prefix + summary
	prefix := pc.prefix()
	summary := pc.summary()
	fmt.Fprintf(&b, " %s  %s %s\n", glyph, headingStyle.Render(prefix), summary)

	// Diff items.
	for _, line := range pc.plan.RenderItemLines() {
		fmt.Fprintf(&b, " %s  %s\n", connStyle.Render("│"), line)
	}

	// Trailing connector.
	fmt.Fprintf(&b, " %s\n", connStyle.Render("│"))

	return b.String()
}

// prefix returns the title prefix for the current state.
func (pc *PlanCard) prefix() string {
	switch pc.state {
	case PlanProposed:
		return "Pending:"
	case PlanApplying:
		return "Applying:"
	case PlanSuccess:
		return "Success:"
	case PlanPartial:
		return "Partial:"
	case PlanFailure:
		return "Failure:"
	case PlanCancelled:
		return "Cancelled:"
	}
	return "Pending:"
}

// summary returns the appropriately-tensed count string for the current state.
func (pc *PlanCard) summary() string {
	switch pc.state {
	case PlanSuccess:
		return pc.plan.SummaryPastTense()
	case PlanPartial:
		return pc.plan.SummaryPartial(pc.succeeded, pc.failed)
	default:
		return pc.plan.Summary()
	}
}

// glyph returns the styled glyph for the current state.
func (pc *PlanCard) glyph() string {
	switch pc.state {
	case PlanProposed:
		return lipgloss.NewStyle().Foreground(Palette.Accent).Render(cardGlyphInput)
	case PlanApplying:
		return lipgloss.NewStyle().Foreground(Palette.Primary).Render(cardGlyphPending)
	case PlanSuccess:
		return lipgloss.NewStyle().Foreground(Palette.Success).Render(cardGlyphSuccess)
	case PlanPartial:
		return lipgloss.NewStyle().Foreground(Palette.Warning).Render(cardGlyphSkipped)
	case PlanFailure:
		return lipgloss.NewStyle().Foreground(Palette.Error).Render(cardGlyphFailed)
	case PlanCancelled:
		return lipgloss.NewStyle().Foreground(Palette.Warning).Render(cardGlyphSkipped)
	}
	return " "
}

// --- BubbleTea model for the applying state ---

type planCardSpinnerModel struct {
	spinner  spinner.Model
	card     *PlanCard
	done     bool
	err      error
	resultCh <-chan planApplyResult
}

type planApplyResult struct {
	err       error
	succeeded int
	failed    int
}

func newPlanCardSpinnerModel(card *PlanCard, resultCh <-chan planApplyResult) planCardSpinnerModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return planCardSpinnerModel{spinner: s, card: card, resultCh: resultCh}
}

func (m planCardSpinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForResult())
}

func (m planCardSpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case planApplyDoneMsg:
		m.done = true
		m.err = msg.result.err
		m.card.SetResults(msg.result.succeeded, msg.result.failed)
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

func (m planCardSpinnerModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	return tea.NewView(m.card.renderWithGlyph(m.spinner.View()))
}

func (m planCardSpinnerModel) waitForResult() tea.Cmd {
	return func() tea.Msg {
		return planApplyDoneMsg{result: <-m.resultCh}
	}
}

type planApplyDoneMsg struct {
	result planApplyResult
}

// RunApply executes the given actions with an animated spinner, transitioning
// the card from Applying to its final state (Success/Partial/Failure).
// Returns nil on full success, or the first error encountered.
func (pc *PlanCard) RunApply(actions []func() error) error {
	pc.SetState(PlanApplying)

	resultCh := make(chan planApplyResult, 1)
	go func() {
		succeeded, failed := 0, 0
		var firstErr error
		for _, action := range actions {
			if err := action(); err != nil {
				failed++
				if firstErr == nil {
					firstErr = err
				}
			} else {
				succeeded++
			}
		}
		resultCh <- planApplyResult{err: firstErr, succeeded: succeeded, failed: failed}
	}()

	p := tea.NewProgram(newPlanCardSpinnerModel(pc, resultCh))
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback — wait for result synchronously.
		pc.Print()
		result := <-resultCh
		pc.SetResults(result.succeeded, result.failed)
		pc.setFinalState(result)
		pc.Print()
		return result.err
	}

	m := model.(planCardSpinnerModel)
	result := planApplyResult{err: m.err, succeeded: pc.succeeded, failed: pc.failed}
	pc.setFinalState(result)
	pc.Print()

	return result.err
}

// setFinalState determines the terminal state based on action results.
func (pc *PlanCard) setFinalState(result planApplyResult) {
	if result.err != nil && result.succeeded == 0 {
		pc.SetState(PlanFailure)
	} else if result.err != nil {
		pc.SetState(PlanPartial)
	} else {
		pc.SetState(PlanSuccess)
	}
}
