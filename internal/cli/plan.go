package cli

import (
	"errors"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ErrCancelled is returned when the user cancels a plan confirmation.
var ErrCancelled = errors.New("cancelled")

// PlanAction is a function that executes one step of a plan.
type PlanAction func() error

// runPlanCard orchestrates the full plan lifecycle: proposed → confirm →
// applying (with spinner) → final state. Returns nil on success,
// ErrCancelled on dry-run/cancel, or the first execution error.
func runPlanCard(cmd *cobra.Command, plan *ui.Plan, actions []PlanAction) error {
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

	// --dry-run: show proposed, exit.
	if isDryRun(cmd) {
		pc.Print() // proposed state (default)
		return ErrCancelled
	}

	// --yes or non-interactive: skip confirmation, go to apply.
	if isAutoApprove(cmd) || !isInteractive() {
		return applyPlanCard(pc, actions)
	}

	// Interactive: show proposed card, confirm with Apply/Cancel.
	rewind := pc.PrintRewindable()

	var confirmed bool
	if err := runForm(
		huh.NewConfirm().
			Title(plan.RenderItems()).
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmed),
	); err != nil {
		rewind()
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return ErrCancelled
	}
	rewind()

	if !confirmed {
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return ErrCancelled
	}

	// Confirmed — apply.
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

// confirmPlan renders the plan and returns whether execution should proceed.
// Deprecated: use runPlanCard instead for the full lifecycle experience.
func confirmPlan(cmd *cobra.Command, plan *ui.Plan) error {
	if plan.IsEmpty() {
		return nil
	}

	if !plan.HasChanges() {
		plan.Print()
		ui.NewCard(ui.CardInfo, "Nothing to do — all items unchanged").Print()
		return nil
	}

	if isDryRun(cmd) {
		plan.Print()
		return ErrCancelled
	}

	if isAutoApprove(cmd) || !isInteractive() {
		plan.Print()
		return nil
	}

	var confirmed bool
	rewind := ui.NewCard(ui.CardInput, plan.Summary()).PrintRewindable()
	if err := runForm(
		huh.NewConfirm().
			Title(plan.RenderItems()).
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmed),
	); err != nil {
		rewind()
		plan.PrintCancelled()
		return ErrCancelled
	}
	rewind()

	if !confirmed {
		plan.PrintCancelled()
		return ErrCancelled
	}

	plan.Print()
	return nil
}

// isAutoApprove returns true if the --yes flag is set.
func isAutoApprove(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("yes")
	return v
}
