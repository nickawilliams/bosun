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
			rootCard(cmd).Print()

			ctx := cmd.Context()

			// --- Pre-flight: migration confirmation ---
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")
			if !migrationsDone {
				if isInteractive() {
					var confirmed bool
					rewind := ui.NewCard(ui.CardInput, "have any required database migrations been run?").Tight().PrintRewindable()
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
						ui.Skip("run migrations first, then use --migrations-done")
						return nil
					}
					ui.Complete("migrations confirmed")
				} else {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
			} else {
				ui.Saved("migrations confirmed", "--migrations-done")
			}

			// --- Plan + Apply ---

			tracker, _ := newIssueTracker()
			var currentStatus string
			if tracker != nil {
				if detail, err := fetchIssue(ctx, tracker, issue); err != nil {
					ui.Fail(fmt.Sprintf("fetching issue: %v", err))
				} else {
					currentStatus = detail.Status
				}
			}

			var actions []Action

			// CI/CD: trigger production deployment.
			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}
			if pipeline != nil {
				targets, _ := resolveWorkflowTargets(ctx, "release")
				inputs, _ := buildWorkflowInputs(cmd, ctx, "release", issue)
				for _, t := range targets {
					target := t
					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						Label:  "trigger production deploy",
						Target: target.Label,
						Assess: func(_ context.Context) (ActionState, string, error) {
							return ActionNeeded, fmt.Sprintf("main → %s", target.Workflow), nil
						},
						Apply: func(ctx context.Context) error {
							return pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
								Owner:      target.Owner,
								Repository: target.Repo,
								Workflow:   target.Workflow,
								Ref:        "main",
								Inputs:     inputs,
							})
						},
					})
				}
			}

			if sa, ok := statusAction(tracker, issue, currentStatus, "done"); ok {
				actions = append(actions, sa)
			}

			return runActions(cmd, ctx, actions)
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	cmd.Flags().StringSlice("service", nil, "service to deploy (can be repeated; overrides auto-detection)")
	return cmd
}
