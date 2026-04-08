package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

			repos := viper.GetStringSlice("repos")
			if flagRepos, _ := cmd.Flags().GetStringSlice("repo"); len(flagRepos) > 0 {
				repos = flagRepos
			}
			if len(repos) == 0 {
				return fmt.Errorf("no repos configured: set repos in .bosun/config.yaml or use --repo")
			}

			// Build the branch name. For now, use the issue directly.
			// Pattern-based naming (branch.pattern config) will be added
			// when issue tracking provides the ticket type and title.
			branchName := issue

			wsRoot := viper.GetString("workspace_root")
			useWorkspaces := wsRoot != ""
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would start work on %s\n", issue)
				fmt.Printf("  Branch: %s\n", branchName)
				fmt.Printf("  Repos: %v\n", repos)
				if useWorkspaces {
					fmt.Printf("  Workspace: %s/%s/\n", wsRoot, branchName)
				}
				fmt.Println("  [stub] Set issue status to In Progress")
				return nil
			}

			ctx := context.Background()

			if useWorkspaces {
				mgr, err := newWorkspaceManager()
				if err != nil {
					return err
				}
				if err := mgr.Create(ctx, branchName, repos, fromHead); err != nil {
					return err
				}
				fmt.Printf("Created workspace for %s\n", issue)
			} else {
				root, err := repoRoot()
				if err != nil {
					return err
				}
				g := git.New()
				for _, repo := range repos {
					repoPath := filepath.Join(root, repo)
					if fromHead {
						err = g.CreateBranchFromHead(ctx, repoPath, branchName)
					} else {
						err = g.CreateBranch(ctx, repoPath, branchName)
					}
					if err != nil {
						return fmt.Errorf("creating branch in %s: %w", repo, err)
					}
					fmt.Printf("Created branch %s in %s\n", branchName, repo)
				}
			}

			// TODO(nick): Set issue status to In Progress (phase 3)
			fmt.Println("[stub] Set issue status to In Progress")

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "repo paths to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
