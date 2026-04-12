package cli

import (
	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// confirmPlan renders the plan and returns whether execution should proceed.
//
//   - --dry-run: render plan, return false
//   - --yes or non-interactive: render plan, return true
//   - interactive: render plan with inline confirm, return answer
//   - empty plan or all unchanged: return true (nothing to confirm)
func confirmPlan(cmd *cobra.Command, plan *ui.Plan) bool {
	if plan.IsEmpty() {
		return true
	}

	// All items are no-ops — nothing to confirm.
	if !plan.HasChanges() {
		plan.Print()
		ui.NewCard(ui.CardInfo, "Nothing to do — all items unchanged").Print()
		return true
	}

	if isDryRun(cmd) {
		plan.Print()
		return false
	}

	if isAutoApprove(cmd) || !isInteractive() {
		plan.Print()
		return true
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
		return false
	}
	rewind()

	// Re-render as static plan.
	plan.Print()

	return confirmed
}

// isAutoApprove returns true if the --yes flag is set.
func isAutoApprove(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("yes")
	return v
}
