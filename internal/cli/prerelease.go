package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newPrereleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prerelease",
		Short: "Prepare release artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			bump, _ := cmd.Flags().GetString("bump")
			ui.Muted("[stub] Would prepare release for %s (bump: %s)", issue, bump)
			ui.Muted("  - Create release/tag per repo")
			ui.Muted("  - Notify release channel")
			ui.Muted("  - Set issue status to Ready for Release")
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")

	return cmd
}
