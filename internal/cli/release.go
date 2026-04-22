package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/cicd"
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

			tracker, _ := newIssueTracker()

			var actions []Action

			// CI/CD: trigger production deployment.
			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}
			workflowName := resolveWorkflowName("release")
			if owner, repo, ok := resolveWorkflowRepository(); ok && pipeline != nil && workflowName != "" {
				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					Label:  "Trigger Production Deploy",
					Target: repo,
					Assess: func(_ context.Context) (ActionState, string, error) {
						return ActionNeeded, fmt.Sprintf("main → %s", workflowName), nil
					},
					Apply: func(ctx context.Context) error {
						return pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
							Owner:      owner,
							Repository: repo,
							Workflow:   workflowName,
							Ref:        "main",
							Inputs:     map[string]string{"issue": issue},
						})
					},
				})
			}

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
