package cli

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Deploy to production",
		Annotations: map[string]string{
			headerAnnotationTitle: "Release",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")

			if isDryRun(cmd) {
				transitionIssueStatus(cmd.Context(), issue, "ready_for_release", "done", true)
				return nil
			}

			// Migration confirmation pre-flight.
			confirmTitle := "Have any required database migrations been run?"
			if !migrationsDone {
				if isInteractive() {
					var confirmed bool
					rewind := ui.NewCard(ui.CardInput, confirmTitle).PrintRewindable()
					if err := runForm(
						huh.NewConfirm().
							Affirmative("Yes").
							Negative("No").
							Value(&confirmed),
					); err != nil {
						return err
					}
					rewind()
					if !confirmed {
						ui.NewCard(ui.CardSkipped, "Run migrations first, then use --migrations-done").Print()
						return nil
					}
					ui.NewCard(ui.CardSuccess, "Migrations confirmed").Print()
				} else {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
			} else {
				ui.NewCard(ui.CardSuccess, "Migrations confirmed").
					Muted("--migrations-done").
					Print()
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
