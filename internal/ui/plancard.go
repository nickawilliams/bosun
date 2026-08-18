package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// PlanCardState represents the lifecycle phase of a plan card.
type PlanCardState int

const (
	PlanProposed  PlanCardState = iota // ? glyph — awaiting confirmation
	PlanVerified                       // ✓ — assess found nothing to do; no apply ran
	PlanApplying                       // spinner — executing actions
	PlanSuccess                        // ✓ — all actions succeeded
	PlanPartial                        // ! — some actions failed
	PlanFailure                        // ✗ — all/critical actions failed
	PlanCancelled                      // ! — user declined
)

// PlanAction is one queued unit of work in a plan apply: the closure
// that performs it, plus the gating that decides whether it runs at
// all.
type PlanAction struct {
	// Run performs the action.
	Run func() error

	// RequiresPriorSuccess gates the action on a clean run so far: it
	// is skipped if any earlier action in the queue failed, or if
	// PriorFailure says something had already failed by the time it
	// was queued.
	//
	// This is for actions whose side effect asserts something about
	// the run as a whole — an issue-tracker transition claims the
	// work is done. Applying one behind a failure publishes a state
	// that never happened, somewhere the error message does not
	// reach. Actions that only describe their own subject (a
	// worktree removal, a per-repo push) leave this false and stay
	// best-effort: independent work should not be held hostage to an
	// unrelated failure.
	RequiresPriorSuccess bool

	// PriorFailure records that the run had already failed before
	// this action was queued — an assessment that errored into a ✗
	// row rather than into the apply queue, so the runner cannot see
	// it by watching Run results.
	//
	// It is a per-action seed rather than a question asked of the
	// whole plan because "prior" has to mean prior: a ✗ row for an
	// action queued *after* the gated one is a later failure, and
	// closing the gate on it would let, say, a notification whose
	// lookup failed withhold a transition for work that succeeded.
	PriorFailure bool

	// Skip, when set, is marked instead of running whenever the gate
	// closes, so the plan row renders as skipped rather than as the
	// change it planned.
	Skip *SkipRef
}

// PlanCard is a stateful plan component that transitions through lifecycle
// states, updating in place. It owns the visual representation of the entire
// plan from proposal through execution.
type PlanCard struct {
	plan      *Plan
	state     PlanCardState
	succeeded int
	failed    int
	skipped   int
}

// NewPlanCard creates a plan card in the Proposed state.
func NewPlanCard(plan *Plan) *PlanCard {
	return &PlanCard{plan: plan, state: PlanProposed}
}

// SetState transitions the card to a new state.
func (pc *PlanCard) SetState(s PlanCardState) {
	pc.state = s
}

// SetResults records how many actions succeeded, failed, and were
// skipped by the gate (for partial/success).
func (pc *PlanCard) SetResults(succeeded, failed, skipped int) {
	pc.succeeded = succeeded
	pc.failed = failed
	pc.skipped = skipped
}

// Render returns the card as a styled string.
func (pc *PlanCard) Render() string {
	return pc.renderWithGlyph(pc.glyph())
}

// Print writes the card to stdout. Suppressed in raw mode.
func (pc *PlanCard) Print() {
	if IsRaw() {
		return
	}
	fmt.Print(spacerPrefix() + pc.Render())
}

// PrintRewindable writes the card to stdout and returns a function that
// erases it via ANSI cursor movement.
func (pc *PlanCard) PrintRewindable() func() {
	prev := needsSpacer
	rendered := spacerPrefix() + pc.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	return func() {
		if lines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
		needsSpacer = prev
	}
}

// renderWithGlyph renders the card with a custom glyph string (used
// by the BubbleTea spinner model to inject animated frames). Builds
// a Card with one Item per plan row so PlanCard renders through the
// same primitive as status repo cards and Plan.Render().
func (pc *PlanCard) renderWithGlyph(glyph string) string {
	card := NewCard(CardInfo, pc.titleWord()).Value(pc.summary())
	pc.plan.AppendItemsToCard(card)
	return card.renderWithGlyph(glyph)
}

// titleWord returns the title word for the current state. The
// " · " separator before the value is added by Card's title row
// rendering when a value is set, so it isn't included here.
func (pc *PlanCard) titleWord() string {
	switch pc.state {
	case PlanProposed:
		return "Pending"
	case PlanVerified:
		return "Verified"
	case PlanApplying:
		return "Applying"
	case PlanSuccess:
		return "Success"
	case PlanPartial:
		return "Partial"
	case PlanFailure:
		return "Failure"
	case PlanCancelled:
		return "Cancelled"
	}
	return "Pending"
}

// summary returns the appropriately-tensed count string for the current state.
func (pc *PlanCard) summary() string {
	switch pc.state {
	case PlanSuccess:
		return pc.plan.SummaryPastTense()
	case PlanPartial:
		return pc.plan.SummaryPartial(pc.succeeded, pc.failed, pc.skipped)
	case PlanFailure:
		// Failure is reached two ways. An apply that failed outright
		// has results to report, and reporting them matters most in
		// exactly the case that motivates the gate: one target, its
		// deploy fails, the transition behind it is skipped. Falling
		// through to Summary() there would caption a "(skipped)" row
		// with the future-tense "1 to update" it no longer plans. The
		// other way in is a plan of nothing but ✗ assess rows, where
		// no apply ran and the op counts are the whole story.
		if pc.succeeded+pc.failed+pc.skipped > 0 {
			return pc.plan.SummaryPartial(pc.succeeded, pc.failed, pc.skipped)
		}
		return pc.plan.Summary()
	default:
		return pc.plan.Summary()
	}
}

// glyph returns the styled glyph for the current state.
func (pc *PlanCard) glyph() string {
	switch pc.state {
	case PlanProposed:
		return lipgloss.NewStyle().Foreground(Palette.Accent).Render(Palette.Unknown)
	case PlanVerified:
		return lipgloss.NewStyle().Foreground(Palette.Success).Render(Palette.Check)
	case PlanApplying:
		return lipgloss.NewStyle().Foreground(Palette.Primary).Render(Palette.Pending)
	case PlanSuccess:
		return lipgloss.NewStyle().Foreground(Palette.Success).Render(Palette.Check)
	case PlanPartial:
		return lipgloss.NewStyle().Foreground(Palette.Warning).Render(Palette.Attention)
	case PlanFailure:
		return lipgloss.NewStyle().Foreground(Palette.Error).Render(Palette.Cross)
	case PlanCancelled:
		return lipgloss.NewStyle().Foreground(Palette.Warning).Render(Palette.Attention)
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
	skipped   int
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
		m.card.SetResults(msg.result.succeeded, msg.result.failed, msg.result.skipped)
		// Set final state so View() renders it as BubbleTea's last
		// frame, avoiding a clear-then-reprint flash.
		m.card.setFinalState(msg.result)
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
		return tea.NewView(m.card.Render())
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
//
// Execution is best-effort by default — every action is attempted and
// the first error is reported at the end — except for actions that set
// RequiresPriorSuccess, which are skipped once anything has failed.
func (pc *PlanCard) RunApply(actions []PlanAction) error {
	// Raw mode: run actions synchronously without BubbleTea.
	if IsRaw() {
		return pc.applyActions(actions).err
	}

	pc.SetState(PlanApplying)

	resultCh := make(chan planApplyResult, 1)
	go func() {
		start := time.Now()
		result := pc.applyActions(actions)
		holdSpinner(start)
		resultCh <- result
	}()

	fmt.Print(spacerPrefix())

	p := tea.NewProgram(newPlanCardSpinnerModel(pc, resultCh))
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback — wait for result synchronously.
		pc.Print()
		result := <-resultCh
		pc.SetResults(result.succeeded, result.failed, result.skipped)
		pc.setFinalState(result)
		pc.Print()
		return result.err
	}

	// BubbleTea's final View() already rendered the finalized plan
	// card in place (state set in Update's planApplyDoneMsg handler).
	m := model.(planCardSpinnerModel)
	return m.err
}

// applyActions runs the queue and tallies the outcome. Shared by the
// raw and spinner paths so the gate can't drift between them.
//
// Two things close the gate, and both are strictly positional: an
// earlier action in this queue returning an error, and the action's own
// PriorFailure seed for a failure that happened before it was queued
// (an assessment that ✗-rowed instead of reaching the queue). A ✗ row
// is a target the run could not even evaluate, which is as good a
// reason not to publish a run-wide claim as an apply that blew up —
// but only if it came first.
//
// Only RequiresPriorSuccess actions consult any of this; everything
// else runs regardless, preserving the best-effort contract the
// independent per-target actions were written against.
func (pc *PlanCard) applyActions(actions []PlanAction) planApplyResult {
	var result planApplyResult
	anyFailed := false

	for _, action := range actions {
		if action.RequiresPriorSuccess && (anyFailed || action.PriorFailure) {
			result.skipped++
			if action.Skip != nil {
				action.Skip.Set()
			}
			continue
		}
		if err := action.Run(); err != nil {
			anyFailed = true
			result.failed++
			if result.err == nil {
				result.err = err
			}
			continue
		}
		result.succeeded++
	}

	return result
}

// setFinalState determines the terminal state based on action results.
//
// A skipped action is enough to deny Success even when everything that
// ran succeeded: the gate only closes behind a failure, so the plan did
// not fully apply. That case is a plan whose failure was an assessment
// (a ✗ row, no apply error) with a gated action behind it — Partial,
// not Success.
func (pc *PlanCard) setFinalState(result planApplyResult) {
	switch {
	case result.err != nil && result.succeeded == 0:
		pc.SetState(PlanFailure)
	case result.err != nil || result.skipped > 0:
		pc.SetState(PlanPartial)
	default:
		pc.SetState(PlanSuccess)
	}
}
