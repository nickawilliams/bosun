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
						notifier, err := newNotifier()
						if err != nil {
							ui.Skip(fmt.Sprintf("Notification: %v", err))
							return nil
						}
						ref, err := notifier.FindThread(ctx, channel, issue)
						if err != nil {
							return fmt.Errorf("finding thread: %w", err)
						}
						if ref.Timestamp == "" {
							ui.Skip(fmt.Sprintf("No notification thread found for %s", issue))
							return nil
						}
						return notifier.ReplyToThread(ctx, ref, notify.Message{
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
