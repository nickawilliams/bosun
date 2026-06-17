package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
			headerAnnotationTitle: "cleanup workspace",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			workspace := cc.Workspace
			force, _ := cmd.Flags().GetBool("force")
			ctx := cmd.Context()
			g := git.New()

			_ = emitLifecyclePreamble(ctx, cc.Issue)

			// Workspace-scoped repos carry the worktree path on
			// Repository.Path; readiness uses that for its dirty/unpushed
			// gather. The main repo path (needed for `git worktree remove`
			// and `git branch -D`) comes from the project config — looked
			// up by name into a parallel map.
			workspaceRepos, err := resolveActiveRepositories(ctx, workspace, nil)
			if err != nil {
				return err
			}
			projectRepos, err := resolveRepositories(nil)
			if err != nil {
				return err
			}
			mainPath := make(map[string]string, len(projectRepos))
			for _, pr := range projectRepos {
				mainPath[pr.Name] = pr.Path
			}

			// --- Pre-flight: Workspace Readiness ---
			//
			// The readiness section's dirty gate replaces the inline
			// dirty check this command used to do. The push offer it
			// folds in is debatable for cleanup (we're about to delete
			// the branch) but harmless if accepted; punting on splitting
			// the helper until the awkwardness shows up in practice.
			readiness, _, err := emitWorkspaceReadiness(ctx, g, workspaceRepos)
			if err != nil {
				return err // ErrCancelled (dirty gate) propagates as a clean abort
			}

			// --- Plan + Apply ---
			var actions []Action

			for i := range readiness {
				rr := &readiness[i]
				repoName := rr.repo.Name
				worktreePath := rr.repo.Path
				repoPath := mainPath[repoName]

				actions = append(actions, Action{
					Op:     ui.PlanDestroy,
					Action: "worktree",
					Type:   "repo",
					Name:   repoName,
					Assess: func(_ context.Context) (ActionState, string, error) {
						if _, err := os.Stat(worktreePath); err != nil {
							return ActionCompleted, workspace, nil
						}
						return ActionNeeded, workspace, nil
					},
					Apply: func(ctx context.Context) error {
						return g.RemoveWorktree(ctx, repoPath, worktreePath, force)
					},
				})
			}

			// Branch deletion uses each worktree's actual checked-out
			// branch (captured by the readiness gather), which handles
			// the case where a worktree has been manually checked out to
			// a branch other than the workspace name.
			for i := range readiness {
				rr := &readiness[i]
				repoName := rr.repo.Name
				repoPath := mainPath[repoName]
				branch := rr.branch

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

			// Post-apply: remove the workspace directory and any empty
			// parents (e.g. "epic/" once the last EX-* workspace under it
			// is cleaned).
			wsRoot := viper.GetString("workspace.root")
			if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}
			wsPath := filepath.Join(wsRoot, workspace)
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

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	return cmd
}
