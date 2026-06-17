package cli

import (
	"fmt"

	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newNotifyTestCmd is a TEMPORARY hidden command for iterating on the
// rendered shape of a notification without creating a real GitHub release
// or transitioning a Jira ticket. Sends a sample message through the real
// buildNotifyContent + notifier path so the rendered output matches what
// prerelease would actually emit. Delete this file (and its registration
// in root.go) once the notification format has been validated on a live
// channel.
func newNotifyTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "notify-test",
		Short:  "Send a sample release notification (TEMPORARY)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			issue, _ := cmd.Flags().GetString("issue")
			channelOverride, _ := cmd.Flags().GetString("channel")
			multi, _ := cmd.Flags().GetBool("multi")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			data := sampleReleaseData(issue, multi)
			content := buildNotifyContent("release", data)

			// Always print the rendered content so the user can iterate
			// on format quickly without sending.
			fmt.Fprintln(ui.Output(), "--- rendered content ---")
			fmt.Fprintln(ui.Output(), content.Text)
			fmt.Fprintln(ui.Output(), "--- end ---")

			if dryRun {
				ui.Skip("dry-run: not sending")
				return nil
			}

			channel := channelOverride
			if channel == "" {
				channel = viper.GetString("notification.channel_release")
			}
			if channel == "" {
				return fmt.Errorf("no channel configured: set --channel or notification.channel_release")
			}

			notifier, err := newNotifier()
			if err != nil {
				return fmt.Errorf("notifier: %w", err)
			}
			defer notifier.Close()

			if _, err := notifier.Notify(ctx, notify.Message{
				Channel:  channel,
				IssueKey: issue,
				Items:    data.Items,
				Content:  content,
			}); err != nil {
				return err
			}
			ui.Complete(fmt.Sprintf("sent release sample to %s", channel))
			return nil
		},
	}
	cmd.Flags().String("issue", "TEST-123", "issue key threaded with the sample notification")
	cmd.Flags().String("channel", "", "override channel (default: notification.channel_release)")
	cmd.Flags().Bool("multi", false, "render two sample items to verify the multi-item separator")
	cmd.Flags().Bool("dry-run", false, "print rendered content without sending")
	return cmd
}

// sampleReleaseData returns a realistic-looking notifyTemplateData with a
// body that mimics GitHub's auto-generated release notes — the shape the
// real prerelease command produces when GenerateNotes is on.
func sampleReleaseData(issue string, multi bool) notifyTemplateData {
	d := notifyTemplateData{
		IssueKey:   issue,
		IssueTitle: "Sample release notification",
		IssueType:  "Story",
		IssueURL:   "https://example.com/browse/" + issue,
		Items: []notify.Item{
			{
				Label:  "sample-service",
				URL:    "https://github.com/example/sample-service/releases/tag/v1.2.4",
				Detail: "v1.2.4",
				// Body mimics what the GitHub adapter produces — raw markdown
				// from generate_release_notes, routed through the same
				// PrettifyReleaseNotes the real CreateRelease applies, so
				// dry-run output matches what a real prerelease run sends.
				Body: gh.PrettifyReleaseNotes("## What's Changed\n" +
					"* " + issue + ": Sample change by @nickawilliams in https://github.com/example/sample-service/pull/42\n" +
					"\n**Full Changelog**: https://github.com/example/sample-service/compare/v1.2.3...v1.2.4"),
			},
		},
	}
	if multi {
		d.Items = append(d.Items, notify.Item{
			// Monorepo shape: multiple services for one repo, joined with
			// `` `, ` `` so the template's surrounding backticks render as
			// `svc-a`, `svc-b`. Mirrors what prerelease produces when a
			// repo's services config + path-map narrows to >1 service.
			Label:  "another-service`, `extra-service",
			URL:    "https://github.com/example/another-service/releases/tag/v0.5.0",
			Detail: "v0.5.0",
			Body: gh.PrettifyReleaseNotes("## What's Changed\n" +
				"* Refactor by @nickawilliams in https://github.com/example/another-service/pull/7\n" +
				"\n**Full Changelog**: https://github.com/example/another-service/compare/v0.4.0...v0.5.0"),
		})
	}
	return d
}
