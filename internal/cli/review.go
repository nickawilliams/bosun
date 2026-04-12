package cli

import (
	"github.com/spf13/cobra"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		Annotations: map[string]string{
			headerAnnotationTitle: "Code review",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			// TODO: Create PRs (phase 4)
			// TODO: Notify (phase 5)

			transitionIssueStatus(cmd.Context(), issue, "in_progress", "review", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
