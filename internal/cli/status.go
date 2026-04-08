package cli

import (
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
			ui.Muted("[stub] Would show status for %s", issue)
			ui.Muted("  - Issue tracker: ticket details + status")
			ui.Muted("  - VCS: branch status per repo")
			ui.Muted("  - Code host: PR status per repo")
			ui.Muted("  - CI/CD: build/deploy status")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
