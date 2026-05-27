package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove workspace and feature branches",
		Annotations: map[string]string{
			headerAnnotationTitle: "cleanup",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Breadcrumb(issue).Print()

			repositories, err := resolveRepositories(nil)
			if err != nil {
				return err
			}

			branchName := issue
			force, _ := cmd.Flags().GetBool("force")
			ctx := cmd.Context()
			g := git.New()

			// --- Pre-flight: dirty check ---
			wsRoot := viper.GetString("workspace.root")
			if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}

			if !force {
				var dirty []string
				for _, r := range repositories {
					wtPath := filepath.Join(wsRoot, branchName, r.Name)
					if _, err := os.Stat(wtPath); err != nil {
						continue // worktree doesn't exist, skip
					}
					if isDirty, err := g.IsDirty(ctx, wtPath); err == nil && isDirty {
						dirty = append(dirty, r.Name)
					}
				}
				if len(dirty) > 0 {
					return fmt.Errorf(
						"repositories have uncommitted changes: %s (use --force to override)",
						strings.Join(dirty, ", "),
					)
				}
			}

			// Capture each worktree's actual current branch up-front,
			// before any RemoveWorktree action runs (which would
			// invalidate the worktree path). This handles the case
			// where a worktree has been manually checked out to a
			// branch other than the issue/workspace name.
			actualBranch := make(map[string]string, len(repositories))
			for _, r := range repositories {
				wtPath := filepath.Join(wsRoot, branchName, r.Name)
				if _, err := os.Stat(wtPath); err != nil {
					actualBranch[r.Name] = branchName
					continue
				}
				if b, err := g.GetCurrentBranch(ctx, wtPath); err == nil && b != "" {
					actualBranch[r.Name] = b
				} else {
					actualBranch[r.Name] = branchName
				}
			}

			// --- Plan + Apply ---
			var actions []Action

			for _, r := range repositories {
				repoPath := r.Path
				repoName := r.Name
				worktreePath := filepath.Join(wsRoot, branchName, repoName)

				actions = append(actions, Action{
					Op:     ui.PlanDestroy,
					Action: "worktree",
					Type:   "repo",
					Name:   repoName,
					Assess: func(_ context.Context) (ActionState, string, error) {
						if _, err := os.Stat(worktreePath); err != nil {
							return ActionCompleted, branchName, nil
						}
						return ActionNeeded, branchName, nil
					},
					Apply: func(ctx context.Context) error {
						return g.RemoveWorktree(ctx, repoPath, worktreePath, force)
					},
				})
			}

			for _, r := range repositories {
				repoPath := r.Path
				repoName := r.Name
				branch := actualBranch[repoName]

				actions = append(actions, Action{
					Op:     ui.PlanDestroy,
					Action: "branch",
					Type:   "repo",
					Name:   repoName,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						exists, err := g.BranchExists(ctx, repoPath, branch)
						if err != nil {
							return 0, "", err
						}
						detail := fmt.Sprintf("%s (local + remote)", branch)
						if !exists {
							return ActionCompleted, detail, nil
						}
						return ActionNeeded, detail, nil
					},
					Apply: func(ctx context.Context) error {
						return g.DeleteBranch(ctx, repoPath, branch)
					},
				})
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			// Post-apply: clean up workspace directory and empty parents.
			wsPath := filepath.Join(wsRoot, branchName)
			if err := os.RemoveAll(wsPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing workspace directory: %w", err)
			}
			for dir := filepath.Dir(wsPath); dir != wsRoot && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) > 0 {
					break
				}
				_ = os.Remove(dir)
			}

			return nil
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	return cmd
}
