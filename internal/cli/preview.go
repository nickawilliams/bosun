package cli

import (
	"context"

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
			previewNotifier, previewNotifierErr := newNotifier()
			if channel != "" && previewNotifierErr == nil {
				var threadRef notify.ThreadRef
				actions = append(actions, Action{
					Op:     ui.PlanModify,
					Label:  "Notify",
					Target: "#" + channel,
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
	return cmd
}
