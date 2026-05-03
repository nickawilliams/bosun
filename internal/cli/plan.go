package cli

import (
	"errors"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ErrCancelled is returned when the user cancels or interrupts.
var ErrCancelled = errors.New("cancelled")

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

	// All items are no-ops — nothing to do.
	if !plan.HasChanges() {
		pc.SetState(ui.PlanSuccess)
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

	// Confirmation required but can't prompt — require --yes.
	if !isInteractive() {
		return fmt.Errorf("confirmation required (pass --yes to approve, or --dry-run to preview)")
	}

	// Interactive confirmation gate: show the plan as a CardInput,
	// run huh confirm. Normal cancel: rewind prompt, show cancelled
	// card in place. Ctrl+c interrupt: don't rewind, just bail.
	rewind := ui.NewCard(ui.CardInput, "pending: "+plan.Summary()).Tight().PrintRewindable()

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
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return ErrCancelled
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
