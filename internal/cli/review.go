package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Header("review", issue)

			if isDryRun(cmd) {
				ui.DryRun("Would submit %s for review", issue)
				ui.Muted("  - Create pull request(s)")
				ui.Muted("  - Notify review channel")
				if statusName, err := resolveStatus("review"); err == nil {
					ui.Item("Status:", fmt.Sprintf("→ %s", statusName))
				}
				return nil
			}

			// TODO: Create PRs (phase 4)
			// TODO: Notify (phase 5)

			ctx := cmd.Context()
			tracker, trackerErr := newIssueTracker()
			if trackerErr != nil {
				ui.Warning("Issue tracker not configured: %v", trackerErr)
			} else {
				statusName, err := resolveStatus("review")
				if err != nil {
					ui.Warning("Status mapping: %v", err)
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "in_progress"); err != nil {
						return err
					}
					err = ui.WithSpinner(fmt.Sprintf("Setting status to %s...", statusName), func() error {
						return tracker.SetStatus(ctx, issue, statusName)
					})
					if err != nil {
						ui.Warning("Failed to set status: %v", err)
					} else {
						ui.Success("Set %s to %s", issue, statusName)
					}
				}
			}

			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
