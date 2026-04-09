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
				transitionIssueStatus(cmd.Context(), issue, "ready_for_release", "done", true)
				return nil
			}

			// Migration confirmation pre-flight.
			if !migrationsDone {
				if isInteractive() {
					if !promptConfirm("Have any required database migrations been run?", false) {
						ui.Skip("Run migrations first, then use --migrations-done")
						return nil
					}
					ui.Complete("Migrations confirmed")
				} else {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
			} else {
				ui.Complete("Migrations confirmed (--migrations-done)")
			}

			// TODO: Trigger production deployment (phase 6)

			transitionIssueStatus(cmd.Context(), issue, "ready_for_release", "done", false)
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	return cmd
}
