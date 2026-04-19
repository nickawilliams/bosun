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
		Annotations: map[string]string{
			headerAnnotationTitle: "release",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()

			// --- Pre-flight: migration confirmation ---
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")
			if !migrationsDone {
				if isInteractive() {
					var confirmed bool
					rewind := ui.NewCard(ui.CardInput, "Have any required database migrations been run?").Tight().PrintRewindable()
					if err := runForm(
						newConfirm().
							Affirmative("Yes").
							Negative("No").
							Value(&confirmed),
					); err != nil {
						return err
					}
					rewind()
					if !confirmed {
						ui.Skip("Run migrations first, then use --migrations-done")
						return nil
					}
					ui.Complete("Migrations confirmed")
				} else {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
			} else {
				ui.Saved("Migrations confirmed", "--migrations-done")
			}

			// --- Plan + Apply ---
			// TODO: Trigger production deployment (phase 6)

			tracker, _ := newIssueTracker()

			var actions []Action
			if sa, ok := statusAction(tracker, issue, "", "done"); ok {
				actions = append(actions, sa)
			}

			return runActions(cmd, ctx, actions)
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	return cmd
}
