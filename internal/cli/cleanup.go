package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/fsutil"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Tear down a workspace's previews, branches, and worktrees",
		Annotations: map[string]string{
			headerAnnotationTitle: "clean up workspace",
		},
		RunE: shellRunE(func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			workspace := cc.Workspace
			force, _ := cmd.Flags().GetBool("force")
			ctx := cmd.Context()
			g := git.New()

			_, _ = emitLifecyclePreamble(ctx, cc.Issue)

			// Workspace-scoped repos carry the worktree path on
			// Repository.Path; the safety check uses that for its
			// dirty / branch-sync / HEAD probes. The main repo path
			// (needed for `git worktree remove` and `git branch -D`)
			// comes from the project config — looked up by name into a
			// parallel map.
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

			// Every destroy action below runs git against the repo's
			// main checkout. A workspace repo the project config no
			// longer matches would look up to an empty path — and git
			// with Dir="" operates on the caller's cwd, aiming
			// `branch -D` / `push origin --delete` at whatever repo
			// the user happens to be standing in. Refuse instead.
			var unmatched []string
			for _, r := range workspaceRepos {
				if mainPath[r.Name] == "" {
					unmatched = append(unmatched, r.Name)
				}
			}
			if len(unmatched) > 0 {
				return fmt.Errorf(
					"workspace repo(s) %s not matched by the project's repositories config; re-add them (or remove the worktrees manually) before cleanup",
					strings.Join(unmatched, ", "))
			}

			wsRoot := viper.GetString("workspace.root")
			if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}
			wsPath := filepath.Join(wsRoot, workspace)

			// --- Pre-flight: Cleanup Readiness ---
			//
			// Workspace-specific safety check (parallel to readiness):
			// classifies each repo + the workspace itself across the
			// safety matrix and gates on the worst severity. BLOCK
			// findings (data loss) exit early unless --force; WARN
			// findings (status mismatches) prompt Continue/Cancel.
			host, _ := newCodeHost()
			tracker, _ := newIssueTracker()
			cleanupRepos, _, err := emitCleanupReadiness(ctx, g, host, tracker, workspaceRepos, wsPath, cc.Issue, force)
			if err != nil {
				return err
			}

			// Resolve the actual branch each worktree is checked out on
			// (handles the "manually checked out a different branch"
			// case). emitCleanupReadiness already captured these; reuse
			// them.
			actualBranch := make(map[string]string, len(cleanupRepos))
			for _, rc := range cleanupRepos {
				actualBranch[rc.repo.Name] = rc.branch
			}

			// --- Plan + Apply ---
			var actions []Action

			// Preview env teardown — first action so the env goes
			// down before any of the local destruction. Idempotent
			// when no env is bound.
			//
			// A provider that won't build is reported, not skipped
			// silently. The workflow-dispatch provider declares no
			// required config so this never failed; one that needs a
			// base URL fails routinely when it isn't set, and cleanup
			// would then destroy the worktrees and leave the env
			// running with nothing said about it.
			provider, providerErr := newPreviewProvider(workspace)
			switch {
			case providerErr != nil:
				ui.Skip(fmt.Sprintf("preview teardown: %v", providerErr))
			case provider != nil && cc.Issue != "":
				ready := &previewReadiness{provider: provider}
				actions = append(actions, cleanupPreviewAction(ctx, ready, provider, cc.Issue))
			}

			for i := range workspaceRepos {
				r := workspaceRepos[i]
				repoName := r.Name
				worktreePath := r.Path
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

			for i := range workspaceRepos {
				r := workspaceRepos[i]
				repoName := r.Name
				repoPath := mainPath[repoName]
				branch := actualBranch[repoName]
				if branch == "" {
					branch = workspace
				}

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
			// parents (e.g. "epic/" once the last EX-* workspace under
			// it is cleaned). Stray files inside wsPath are gated by
			// the safety check; by the time we reach here they've been
			// explicitly acknowledged (--force) and we destroy them.
			if err := os.RemoveAll(wsPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing workspace directory: %w", err)
			}
			for dir := filepath.Dir(wsPath); dir != wsRoot && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
				entries, err := os.ReadDir(dir)
				if err != nil || fsutil.HasMeaningfulEntries(entries) {
					break
				}
				// Junk-only (or empty) parent: RemoveAll so a lingering
				// .DS_Store doesn't leave the directory behind.
				_ = os.RemoveAll(dir)
			}

			return nil
		}),
	}

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "bypass cleanup-readiness blockers (uncommitted changes, unmerged work, stray files)")
	return cmd
}

// cleanupPreviewAction builds the preview-env teardown row in
// cleanup's action plan. Assess checks whether an env is bound; no
// env → ActionCompleted (the row still appears, marked as already
// done). Apply tears down via the provider — idempotent on the
// adapter side, so a stale registry entry doesn't cause failure.
//
// The provider is asked whether it can tear down at all, the same way
// `bosun preview` asks. This is the second of the two places that call
// Destroy, and until it asked, "the provider answers for itself
// whether each half of its lifecycle is wired" held in one of them:
// an unwired provider got an operative row here that failed at apply,
// after cleanup had already destroyed the worktrees.
//
// Readiness is settled here rather than inside Assess because the
// reason travels on ui.Skip, and Assess runs inside the assessment
// spinner. The cost is that a run with no env bound still hears why a
// teardown it did not need could not have happened; the alternative is
// a reason that arrives inside a spinner or not at all.
func cleanupPreviewAction(ctx context.Context, ready *previewReadiness, provider preview.Provider, issueKey string) Action {
	reason, err := ready.reason(ctx, preview.OpDestroy)
	switch {
	case err != nil:
		return failedAction(ui.PlanDestroy, "teardown", issueKey, err)
	case reason != "":
		return noopAction("teardown", issueKey, reason)
	}

	var envName string
	return Action{
		Op:     ui.PlanDestroy,
		Action: "teardown",
		Type:   "env",
		Name:   issueKey,
		Assess: func(ctx context.Context) (ActionState, string, error) {
			env, err := provider.Get(ctx, issueKey)
			if err != nil {
				if errors.Is(err, preview.ErrNoEnvironment) {
					return ActionCompleted, "(none)", nil
				}
				// Probe failure (network, indeterminate) — still
				// attempt teardown so a registry entry doesn't strand.
				// envName keeps the API value (possibly empty for the
				// provider to resolve); only the plan detail shows the
				// "(unknown)" placeholder.
				envName = env.Name
				detail := envName
				if detail == "" {
					detail = "(unknown)"
				}
				return ActionNeeded, detail, nil
			}
			envName = env.Name
			return ActionNeeded, envName, nil
		},
		Apply: func(ctx context.Context) error {
			return provider.Destroy(ctx, issueKey, envName)
		},
	}
}
