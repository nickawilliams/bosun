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
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Header("status", issue)

			tracker, err := newIssueTracker()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			detail, err := ui.WithSpinnerResult("Fetching issue...", func() (issuepkg.Issue, error) {
				return tracker.GetIssue(ctx, issue)
			})
			if err != nil {
				return err
			}

			kv := ui.NewKV().
				Add("Title", detail.Title).
				Add("Status", detail.Status).
				Add("Type", detail.Type).
				Add("URL", detail.URL)

			ui.NewPanel(detail.Key).
				Content(kv.Render()).
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
