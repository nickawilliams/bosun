package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Begin work on an issue",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			fmt.Printf("[stub] Would start work on %s\n", issue)
			fmt.Println("  - Create branch in target repo(s)")
			fmt.Println("  - Create workspace (if configured)")
			fmt.Println("  - Set issue status to In Progress")
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "repo paths to operate on")

	return cmd
}
