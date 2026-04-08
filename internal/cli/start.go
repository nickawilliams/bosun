package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
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

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			branchName := issue
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				ui.DryRun("Would start work on %s", issue)
				ui.Item("Branch:", branchName)
				ui.Item("Repos:", repoNames(repos))
				ui.Muted("  [stub] Set issue status to In Progress")
				return nil
			}

			// Confirm when operating on multiple unfiltered repos.
			if len(repos) > 1 && len(filterRepos) == 0 {
				label := fmt.Sprintf("Start %s in %d repos (%s)?",
					issue, len(repos), repoNames(repos))
				if !promptConfirm(label, true) {
					ui.Warning("Aborted.")
					return nil
				}
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			err = ui.WithSpinner("Creating workspace...", func() error {
				return mgr.Create(context.Background(), branchName, wsRepos, fromHead)
			})
			if err != nil {
				return err
			}

			ui.Success("Created workspace for %s", issue)
			for _, r := range repos {
				ui.Item(r.Name, r.Path)
			}

			// TODO(nick): Set issue status to In Progress (phase 3)
			ui.Muted("  [stub] Set issue status to In Progress")

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
