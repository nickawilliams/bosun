package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Deploy to production",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")
			ui.Muted("[stub] Would release %s (migrations-done: %v)", issue, migrationsDone)
			ui.Muted("  - Confirm migrations (if applicable)")
			ui.Muted("  - Trigger production deployment")
			ui.Muted("  - Set issue status to Done")
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")

	return cmd
}
