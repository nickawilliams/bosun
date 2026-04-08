package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove workspace and feature branches",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			fmt.Printf("[stub] Would clean up %s\n", issue)
			fmt.Println("  - Remove worktrees")
			fmt.Println("  - Delete local and remote feature branches")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
