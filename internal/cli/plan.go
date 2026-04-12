package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// confirmPlan renders the plan and returns whether execution should proceed.
//
//   - --dry-run: render plan, return false
//   - --yes or non-interactive: render plan, return true
//   - interactive: render plan, prompt "Apply? [Y/n]", return answer
//   - empty plan or all unchanged: return true (nothing to confirm)
func confirmPlan(cmd *cobra.Command, plan *ui.Plan) bool {
	if plan.IsEmpty() {
		return true
	}

	plan.Print()

	// All items are no-ops — nothing to confirm.
	if !plan.HasChanges() {
		ui.NewCard(ui.CardInfo, "Nothing to do — all items unchanged").Print()
		return true
	}

	if isDryRun(cmd) {
		return false
	}

	if isAutoApprove(cmd) || !isInteractive() {
		return true
	}

	return promptConfirm("Apply?", true)
}

// isAutoApprove returns true if the --yes flag is set.
func isAutoApprove(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("yes")
	return v
}
