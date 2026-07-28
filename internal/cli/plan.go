package cli

import (
	"errors"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ErrCancelled is returned when the user cancels or interrupts.
var ErrCancelled = errors.New("cancelled")

// errPlanCancelled marks a cancellation the plan card has already
// rendered — the Cancelled-state card is on screen, so HandleError
// suppresses its trailing "user cancelled" card (which would say the
// same thing twice) while the non-zero exit and abort semantics are
// preserved. Wraps ErrCancelled so every existing errors.Is check
// still matches.
var errPlanCancelled = fmt.Errorf("plan %w", ErrCancelled)

// PlanAction is a function that executes one step of a plan.
type PlanAction func() error

// PlanOpts controls the two independent axes of Plan Card behavior.
type PlanOpts struct {
	// Confirm enables the interactive confirmation gate between
	// proposed and apply. Default: on for Phase.Plan commands (lifecycle);
	// off for direct mutating Tasks (workspace).
	Confirm bool

	// Apply enables execution of actions after the proposed state.
	// When false the plan renders in proposed state and returns
	// ErrCancelled without mutating. --dry-run sets this to false.
	Apply bool
}

// DefaultPlanOpts returns the standard axes for lifecycle commands:
// confirmation on, apply gated by --dry-run.
func DefaultPlanOpts(cmd *cobra.Command) PlanOpts {
	return PlanOpts{
		Confirm: true,
		Apply:   !isDryRun(cmd),
	}
}

// runPlanCard orchestrates the full plan lifecycle: proposed → confirm →
// applying (with spinner) → final state. Returns nil on success,
// ErrCancelled on dry-run/cancel/interrupt, or the first execution error.
func runPlanCard(cmd *cobra.Command, plan *ui.Plan, actions []PlanAction, opts PlanOpts) error {
	if plan.IsEmpty() {
		return nil
	}

	pc := ui.NewPlanCard(plan)

	// All items are no-ops — nothing to do. PlanVerified is a sibling
	// of PlanSuccess (same ✓ glyph + success color) with a different
	// title word — "Success" carries an action-verb feel that misreads
	// when no work was performed; "Verified" affirms the check happened
	// and the state already matches what was wanted.
	if !plan.HasChanges() {
		pc.SetState(ui.PlanVerified)
		pc.Print()
		return nil
	}

	// Apply disabled (--dry-run): show proposed, exit.
	if !opts.Apply {
		pc.Print()
		return ErrCancelled
	}

	// No confirmation gate: straight to apply.
	if !opts.Confirm || isAutoApprove(cmd) {
		return applyPlanCard(pc, actions)
	}

	// Confirmation required but we can't surface a form to a human:
	// either stdin can't be read (CI / piped input) or stdout can't
	// render the prompt visibly (piped stdout). Require --yes.
	if !isInteractive() || !ui.CanRenderInteractively() {
		return fmt.Errorf("confirmation required (pass --yes to approve, or --dry-run to preview)")
	}

	// Interactive confirmation gate: show the plan as a CardInput,
	// run huh confirm. Normal cancel: rewind prompt, show cancelled
	// card in place. Ctrl+c interrupt: don't rewind, just bail.
	rewind := newPlanPendingHeader(plan).PrintRewindable()

	var confirmed bool
	err := runForm(
		newConfirm().
			Title(plan.RenderItems()).
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmed),
	)

	if err != nil {
		return ErrCancelled
	}

	rewind()

	if !confirmed {
		// The Cancelled plan card IS the cancellation record —
		// errPlanCancelled tells HandleError not to add another.
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return errPlanCancelled
	}

	return applyPlanCard(pc, actions)
}

// applyPlanCard runs actions with an animated spinner, transitioning the
// card through applying → success/partial/failure.
func applyPlanCard(pc *ui.PlanCard, actions []PlanAction) error {
	wrappedActions := make([]func() error, len(actions))
	for i, a := range actions {
		wrappedActions[i] = a
	}
	return pc.RunApply(wrappedActions)
}

// newPlanPendingHeader builds the title-bar-only card shown above
// the confirmation form during runPlanCard's interactive gate. Both
// runPlanCard and the demo snapshot route through this so the
// pending header can't visually drift from the live render.
func newPlanPendingHeader(plan *ui.Plan) *ui.Card {
	return ui.NewCard(ui.CardInput, "Pending").Value(plan.Summary()).Tight()
}

// isAutoApprove returns true if the --yes or --force flag is set. Commands
// that don't define one of the flags get a false from cobra (with an
// ignored error), so the check is safe to apply uniformly.
func isAutoApprove(cmd *cobra.Command) bool {
	if v, _ := cmd.Flags().GetBool("yes"); v {
		return true
	}
	if v, _ := cmd.Flags().GetBool("force"); v {
		return true
	}
	return false
}
