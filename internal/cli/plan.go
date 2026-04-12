package cli

import (
	"errors"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ErrCancelled is returned when the user cancels a plan confirmation.
var ErrCancelled = errors.New("cancelled")

// confirmPlan renders the plan and returns whether execution should proceed.
// Returns ErrCancelled if the user declines. Returns nil to proceed.
//
//   - --dry-run: render plan, return ErrCancelled (no apply)
//   - --yes or non-interactive: render plan, return nil (proceed)
//   - interactive: render plan with inline confirm, return based on answer
//   - empty plan or all unchanged: return nil (nothing to confirm)
func confirmPlan(cmd *cobra.Command, plan *ui.Plan) error {
	if plan.IsEmpty() {
		return nil
	}

	// All items are no-ops — nothing to confirm.
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

	// Interactive: the plan content becomes the confirm prompt.
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
