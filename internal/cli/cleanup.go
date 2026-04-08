package cli

import (
	"context"

	"github.com/nickawilliams/bosun/internal/ui"
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
				ui.DryRun("Would clean up %s", issue)
				ui.Item("Workspace:", branchName)
				ui.Item("Repos:", repoNames(repos))
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			err = ui.WithSpinner("Removing workspace...", func() error {
				return mgr.Remove(context.Background(), branchName, wsRepos, force)
			})
			if err != nil {
				return err
			}

			ui.Success("Cleaned up %s", issue)
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
