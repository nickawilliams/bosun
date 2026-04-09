package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Deploy to production",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Header("release", issue)
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")

			if isDryRun(cmd) {
				ui.DryRun("Would release %s (migrations-done: %v)", issue, migrationsDone)
				ui.Muted("  - Confirm migrations (if applicable)")
				ui.Muted("  - Trigger production deployment")
				if statusName, err := resolveStatus("done"); err == nil {
					ui.Item("Status:", fmt.Sprintf("→ %s", statusName))
				}
				return nil
			}

			// Migration confirmation pre-flight.
			if !migrationsDone {
				if isInteractive() {
					if !promptConfirm("Have any required database migrations been run?", false) {
						ui.Warning("Run migrations first, then use --migrations-done")
						return nil
					}
				} else {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
			}

			// TODO: Trigger production deployment (phase 6)

			ctx := cmd.Context()
			tracker, trackerErr := newIssueTracker()
			if trackerErr != nil {
				ui.Warning("Issue tracker not configured: %v", trackerErr)
			} else {
				statusName, err := resolveStatus("done")
				if err != nil {
					ui.Warning("Status mapping: %v", err)
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "ready_for_release"); err != nil {
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
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")

	return cmd
}
