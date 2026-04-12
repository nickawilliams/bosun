package cli

import (
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show issue lifecycle status",
		Annotations: map[string]string{
			headerAnnotationTitle: "Issue status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			tracker, err := newIssueTracker()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			var detail issuepkg.Issue
			if err := ui.RunCard("Fetching issue", func() error {
				var fetchErr error
				detail, fetchErr = tracker.GetIssue(ctx, issue)
				return fetchErr
			}); err != nil {
				return err
			}

			ui.NewCard(ui.CardInfo, detail.Key).
				KV(
					"Title", detail.Title,
					"Status", detail.Status,
					"Type", detail.Type,
					"URL", detail.URL,
				).
				Print()

			// TODO: VCS branch status per repo (phase 2 data available)
			// TODO: Code host PR status (phase 4)
			// TODO: CI/CD status (phase 6)

			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
