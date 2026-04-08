package cli

import (
	"fmt"

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
			fmt.Printf("[stub] Would prepare release for %s (bump: %s)\n", issue, bump)
			fmt.Println("  - Create release/tag per repo")
			fmt.Println("  - Notify release channel")
			fmt.Println("  - Set issue status to Ready for Release")
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")

	return cmd
}
