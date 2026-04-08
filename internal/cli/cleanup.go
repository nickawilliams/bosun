package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

			repos := viper.GetStringSlice("repos")
			if len(repos) == 0 {
				return fmt.Errorf("no repos configured: set repos in .bosun/config.yaml")
			}

			branchName := issue
			force, _ := cmd.Flags().GetBool("force")

			wsRoot := viper.GetString("workspace_root")
			useWorkspaces := wsRoot != ""

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would clean up %s\n", issue)
				if useWorkspaces {
					fmt.Printf("  Remove workspace: %s/%s/\n", wsRoot, branchName)
				}
				fmt.Printf("  Delete branches: %s in %v\n", branchName, repos)
				return nil
			}

			ctx := context.Background()

			if useWorkspaces {
				mgr, err := newWorkspaceManager()
				if err != nil {
					return err
				}
				if err := mgr.Remove(ctx, branchName, force); err != nil {
					return err
				}
				fmt.Printf("Removed workspace for %s\n", issue)
			} else {
				root, err := repoRoot()
				if err != nil {
					return err
				}
				g := git.New()
				for _, repo := range repos {
					repoPath := filepath.Join(root, repo)
					if err := g.DeleteBranch(ctx, repoPath, branchName); err != nil {
						return fmt.Errorf("deleting branch in %s: %w", repo, err)
					}
					fmt.Printf("Deleted branch %s in %s\n", branchName, repo)
				}
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
