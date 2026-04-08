package cli

import (
	"context"
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

			repos, err := resolveRepos(nil)
			if err != nil {
				return err
			}

			branchName := issue
			force, _ := cmd.Flags().GetBool("force")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would clean up %s\n", issue)
				fmt.Printf("  Remove workspace: %s\n", branchName)
				fmt.Printf("  Delete branches in: %s\n", repoNames(repos))
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			if err := mgr.Remove(context.Background(), branchName, wsRepos, force); err != nil {
				return err
			}

			fmt.Printf("Cleaned up %s\n", issue)
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
