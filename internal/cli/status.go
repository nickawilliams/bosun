package cli

import (
	"fmt"

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
			fmt.Printf("[stub] Would show status for %s\n", issue)
			fmt.Println("  - Issue tracker: ticket details + status")
			fmt.Println("  - VCS: branch status per repo")
			fmt.Println("  - Code host: PR status per repo")
			fmt.Println("  - CI/CD: build/deploy status")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
