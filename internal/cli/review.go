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
			ui.Header("review", issue)

			// TODO: Create PRs (phase 4)
			// TODO: Notify (phase 5)

			transitionIssueStatus(cmd.Context(), issue, "in_progress", "review", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
