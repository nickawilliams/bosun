package cli

import (
	"fmt"

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
			fmt.Printf("[stub] Would submit %s for review\n", issue)
			fmt.Println("  - Create pull request(s)")
			fmt.Println("  - Notify review channel")
			fmt.Println("  - Set issue status to Review")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
