package cli

import (
	"context"
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

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Build the branch name. For now, use the issue directly.
			// Pattern-based naming (branch.pattern config) will be added
			// when issue tracking provides the ticket type and title.
			branchName := issue
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would start work on %s\n", issue)
				fmt.Printf("  Branch: %s\n", branchName)
				fmt.Printf("  Repos: %s\n", repoNames(repos))
				fmt.Println("  [stub] Set issue status to In Progress")
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			if err := mgr.Create(context.Background(), branchName, wsRepos, fromHead); err != nil {
				return err
			}

			fmt.Printf("Created workspace for %s\n", issue)
			for _, r := range repos {
				fmt.Printf("  %s → %s\n", r.Name, r.Path)
			}

			// TODO(nick): Set issue status to In Progress (phase 3)
			fmt.Println("[stub] Set issue status to In Progress")

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
