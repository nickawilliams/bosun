package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/notify"
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

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			// Post-apply: reply to review notification thread.
			channel := viper.GetString("slack.channel_review")
			replyToNotification(ctx, channel, issue, notify.Message{
				IssueKey: issue,
				Summary:  fmt.Sprintf("Preview deployment requested for %s", issue),
			})

			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
