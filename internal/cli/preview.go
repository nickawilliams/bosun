package cli

import (
	"context"
	"fmt"

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
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()
			tracker, _ := newIssueTracker()

			// --- Plan + Apply ---
			// TODO: Trigger deployment (phase 6)

			var actions []Action
			if sa, ok := statusAction(tracker, issue, "", "preview"); ok {
				actions = append(actions, sa)
			}

			channel := viper.GetString("slack.channel_review")
			if channel != "" {
				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					Label:  "Notify",
					Target: "#" + channel,
					Assess: func(_ context.Context) (ActionState, string, error) {
						return ActionNeeded, "reply to review thread", nil
					},
					Apply: func(ctx context.Context) error {
						summary := fmt.Sprintf("Preview deployment requested for %s", issue)
						if body := buildNotifyBody("slack.preview_template", notifyTemplateData{
							IssueKey: issue,
						}); body != "" {
							summary = body
						}
						replyToNotification(ctx, channel, issue, notify.Message{
							IssueKey: issue,
							Summary:  summary,
						})
						return nil
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
	return cmd
}
