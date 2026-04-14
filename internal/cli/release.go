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

			// --- Plan + Apply ---
			plan := ui.NewPlan()
			addStatusPlanItem(plan, issue, "", "done")
			// TODO: Trigger production deployment (phase 6)

			statusName, _ := resolveStatus("done")
			tracker, trackerErr := newIssueTracker()

			var actions []PlanAction
			if trackerErr == nil && statusName != "" {
				actions = append(actions, func() error {
					return tracker.SetStatus(ctx, issue, statusName)
				})
			}

			if err := runPlanCard(cmd, plan, actions); err != nil {
				return err
			}
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	return cmd
}
