package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			fmt.Printf("[stub] Would deploy %s to preview\n", issue)
			fmt.Println("  - Trigger ephemeral environment deployment")
			fmt.Println("  - Reply to review notification with preview URL")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
