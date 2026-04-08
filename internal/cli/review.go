package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Muted("[stub] Would submit %s for review", issue)
			ui.Muted("  - Create pull request(s)")
			ui.Muted("  - Notify review channel")
			ui.Muted("  - Set issue status to Review")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
