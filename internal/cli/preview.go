package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		Annotations: map[string]string{
			headerAnnotationTitle: "deploy",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Print()

			ctx := cmd.Context()
			tracker, _ := newIssueTracker()
			if tracker != nil {
				if _, err := fetchIssue(ctx, tracker, issue); err != nil {
					ui.Fail(fmt.Sprintf("fetching issue: %v", err))
				}
			}

			// --- Plan + Apply ---

			var actions []Action

			// CI/CD: trigger preview deployment.
			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}
			if pipeline != nil {
				targets, _ := resolveWorkflowTargets(ctx, "preview")
				inputs, _ := buildWorkflowInputs(cmd, ctx, issue)
				for _, t := range targets {
					target := t
					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						Label:  "trigger preview deploy",
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

			if sa, ok := statusAction(tracker, issue, "", "preview"); ok {
				actions = append(actions, sa)
			}

			channel := viper.GetString("slack.channel_review")
			previewNotifier, previewNotifierErr := newNotifier()
			if previewNotifierErr == nil {
				defer previewNotifier.Close()
			}
			if channel != "" && previewNotifierErr == nil {
				var threadRef notify.ThreadRef
				actions = append(actions, Action{
					Op:     ui.PlanModify,
					Label:  "notify",
					Target: channel,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						ref, _ := previewNotifier.FindThread(ctx, channel, issue)
						if ref.Timestamp == "" {
							return ActionSkipped, "", nil
						}
						threadRef = ref
						return ActionNeeded, "reply to review thread", nil
					},
					Apply: func(ctx context.Context) error {
						return previewNotifier.ReplyToThread(ctx, threadRef, notify.Message{
							IssueKey: issue,
							Content: buildNotifyContent("preview", notifyTemplateData{
								IssueKey: issue,
							}),
						})
					},
				})
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("service", nil, "service to deploy (can be repeated; overrides auto-detection)")
	return cmd
}

// buildWorkflowInputs constructs the inputs map for a workflow dispatch.
// Always includes the issue key. If a service_input is configured, resolves
// service names from the --service flag or the services config.
func buildWorkflowInputs(cmd *cobra.Command, ctx context.Context, issue string) (map[string]string, error) {
	inputs := map[string]string{"issue": issue}

	inputName := serviceInputName()
	if inputName == "" {
		return inputs, nil
	}

	// --service flag overrides auto-detection.
	flagServices, _ := cmd.Flags().GetStringSlice("service")
	if len(flagServices) > 0 {
		inputs[inputName] = strings.Join(flagServices, ",")
		return inputs, nil
	}

	// Auto-detect from workspace repos + services config.
	services, err := resolveServiceNames(ctx)
	if err != nil {
		return inputs, err
	}
	if len(services) > 0 {
		inputs[inputName] = strings.Join(services, ",")
	}

	return inputs, nil
}
