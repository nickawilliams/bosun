package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Begin work on an issue",
		Annotations: map[string]string{
			headerAnnotationTitle: "start work",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireIssue(); err != nil {
				return err
			}
			issue := cc.Issue

			ctx := cmd.Context()
			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			fromHead, _ := cmd.Flags().GetBool("from-head")

			// --- Resolve ---

			// Story card + future meta cards via the shared lifecycle
			// preamble. Tracker outages render a Failed card and
			// continue with zero detail — start gracefully degrades by
			// using the issue key as the branch name (no slug prompt).
			detail := emitLifecyclePreamble(ctx, issue)

			// One workspace per issue: if a workspace already exists
			// for this issue, reuse its name as the branch name and
			// skip the slug prompt + buildBranchName entirely. The
			// per-repo branch/worktree actions below assess each repo
			// independently and no-op when nothing's needed, so the
			// "reuse" path lands as setup-checks-only followed by the
			// tracker transition.
			//
			// Where we got the issue (flag / picker / cwd) doesn't
			// affect this — the issue key is the lookup key.
			existingWorkspace := ""
			if mgr, err := newWorkspaceManager(); err == nil {
				if names, err := mgr.List(); err == nil {
					for _, name := range names {
						if extractIssue(filepath.Base(name)) == issue {
							existingWorkspace = name
							break
						}
					}
				}
			}

			var branchName string
			if existingWorkspace != "" {
				branchName = existingWorkspace
			} else {
				// Resolve slug for branch naming.
				slugOverride, _ := cmd.Flags().GetString("slug")
				var slug string
				if detail.Key != "" {
					switch {
					case slugOverride != "":
						slug = slugify(slugOverride)
					case isInteractive():
						suggested := slugify(detail.Title)
						input, field := newDefaultInput(suggested)
						slugSlot := ui.NewSlot()
						slugSlot.Show(ui.NewCard(ui.CardInput, "branch slug").Tight())
						if err := runForm(input); err != nil {
							return err
						}
						slugSlot.Clear()
						slug = field.Resolved()
						if slug != suggested {
							slug = slugify(slug)
						}
						ui.Selected("branch slug", slug)
					}
				}

				// Build branch name.
				branchName = issue
				if detail.Key != "" {
					name, err := buildBranchName(detail.Key, detail.Type, detail.Title, slug)
					if err != nil {
						ui.Skip(fmt.Sprintf("branch naming: %v (using %s)", err, issue))
					} else {
						branchName = name
					}
				}
			}

			// Resolve repositories.
			repositories, err := resolveRepositories(filterRepositories)
			if err != nil {
				return err
			}

			// Reusing an existing workspace: scope to repos already in
			// it. The configured repo set may have grown since the
			// workspace was created (new repos added to project
			// config), but expanding the workspace as a side-effect of
			// `start` is the wrong verb — that's what `workspace add`
			// is for. Without this filter, `start` on an existing
			// workspace silently produces a plan that provisions
			// branches + worktrees for every newly-configured repo.
			//
			// resolveActiveRepositories enforces the --repository
			// filter against the workspace's actual repo list, so
			// `bosun start --repository=NotInWorkspace` surfaces as
			// "no repositories matched filter (workspace repos: …)"
			// rather than silently adding NotInWorkspace.
			if existingWorkspace != "" {
				active, err := resolveActiveRepositories(ctx, existingWorkspace, filterRepositories)
				if err != nil {
					return err
				}
				activeNames := make(map[string]bool, len(active))
				for _, a := range active {
					activeNames[a.Name] = true
				}
				filtered := repositories[:0]
				for _, r := range repositories {
					if activeNames[r.Name] {
						filtered = append(filtered, r)
					}
				}
				repositories = filtered
			}

			// Interactive repository selection — only for fresh
			// workspaces. Reusing an existing one shouldn't re-ask the
			// user to pick repos; membership is already settled and
			// the per-repo assess will no-op the existing entries.
			if existingWorkspace == "" && len(repositories) > 1 && len(filterRepositories) == 0 && isInteractive() {
				opts := make([]huh.Option[string], len(repositories))
				for i, r := range repositories {
					opts[i] = huh.NewOption(r.Name, r.Name)
				}

				var selected []string
				repositorySlot := ui.NewSlot()
				repositorySlot.Show(ui.NewCard(ui.CardInput, "repositories").Tight())
				if err := runForm(
					huh.NewMultiSelect[string]().
						Options(opts...).
						Value(&selected),
				); err != nil {
					return err
				}
				repositorySlot.Clear()

				if len(selected) == 0 {
					ui.Skip("no repositories selected")
					return nil
				}

				ui.SelectedMulti("repositories", selected)

				repositories, err = resolveRepositories(selected)
				if err != nil {
					return err
				}
			}

			// Compute workspace root for worktree path display.
			projectRoot := config.FindProjectRoot()
			wsRoot := viper.GetString("workspace.root")
			if !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}

			// --- Plan + Apply ---

			cwd, _ := os.Getwd()
			g := git.New()
			var actions []Action

			// Per-repo branch name. For an existing workspace, the truth
			// is what each worktree is actually checked out on — not the
			// workspace name. Heterogeneous workspaces (the user manually
			// checked out a different branch in one worktree, or curated
			// the workspace with per-repo custom branches) need each
			// repo's actions to key off the repo's real HEAD; otherwise
			// the branch action sees the workspace-name branch as missing
			// and creates a dangling sibling. Mirrors cleanup.go's
			// actualBranch pattern.
			//
			// Fresh workspaces (no worktree yet) and any repo whose
			// branch can't be determined fall back to the workspace name
			// — the creation-time default.
			actualBranch := make(map[string]string, len(repositories))
			for _, r := range repositories {
				actualBranch[r.Name] = branchName
				if existingWorkspace == "" {
					continue
				}
				wtPath := filepath.Join(wsRoot, branchName, r.Name)
				if b, err := g.GetCurrentBranch(ctx, wtPath); err == nil && b != "" {
					actualBranch[r.Name] = b
				}
			}

			// Per-repo branch actions, then worktree actions (order matters:
			// branches must exist before worktrees can be created).
			for _, r := range repositories {
				repoPath := r.Path
				repoName := r.Name
				branch := actualBranch[repoName]

				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					Action: "branch",
					Type:   "repo",
					Name:   repoName,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						exists, err := g.BranchExists(ctx, repoPath, branch)
						if err != nil {
							return 0, "", err
						}
						if exists {
							return ActionCompleted, branch, nil
						}
						return ActionNeeded, branch, nil
					},
					Apply: func(ctx context.Context) error {
						if fromHead {
							return g.CreateBranchFromHead(ctx, repoPath, branch)
						}
						return g.CreateBranch(ctx, repoPath, branch)
					},
				})
			}

			for _, r := range repositories {
				repoPath := r.Path
				repoName := r.Name
				worktreePath := filepath.Join(wsRoot, branchName, repoName)

				displayPath := worktreePath
				if rel, err := filepath.Rel(cwd, worktreePath); err == nil {
					displayPath = rel
				}

				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					Action: "worktree",
					Type:   "repo",
					Name:   repoName,
					Assess: func(_ context.Context) (ActionState, string, error) {
						if _, err := os.Stat(worktreePath); err == nil {
							return ActionCompleted, displayPath, nil
						}
						return ActionNeeded, displayPath, nil
					},
					Apply: func(ctx context.Context) error {
						if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
							return fmt.Errorf("creating workspace directory: %w", err)
						}
						return g.CreateWorktree(ctx, repoPath, worktreePath, branchName)
					},
				})
			}

			tracker, _ := newIssueTracker()
			if sa, ok := statusAction(tracker, issue, detail.Status, "in_progress"); ok {
				actions = append(actions, sa)
			}

			return runActions(cmd, ctx, actions)
		},
	}

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().String("slug", "", "custom slug for branch name")
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
