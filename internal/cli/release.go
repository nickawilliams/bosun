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
			headerAnnotationTitle: "release",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			// --- Pre-flight: migration confirmation ---
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")
			if !migrationsDone {
				if isInteractive() {
					var confirmed bool
					rewind := ui.NewCard(ui.CardInput, "Have any required database migrations been run?").PrintRewindable()
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

			// --- Resolve ---
			statusName, statusErr := resolveStatus("done")

			// --- Plan ---
			plan := ui.NewPlan()
			if statusErr == nil {
				plan.Add(ui.PlanModify, "Update Issue Status", issue, fmt.Sprintf("→ %s", statusName))
			}
			// TODO: Trigger production deployment (phase 6)

			if !confirmPlan(cmd, plan) {
				return nil
			}

			// --- Apply ---
			transitionIssueStatus(cmd.Context(), issue, "ready_for_release", "done")
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	return cmd
}
