package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/config"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
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

			// Fetch issue details for branch naming. Render the same
			// Story card shape that `bosun status` workspace mode uses
			// (type + linked key + bold title + colored status parens)
			// so a user moving from status → start sees the same
			// summary of what they're about to work on.
			var detail issuepkg.Issue
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				err := ui.RunCardReplace("", func() error {
					d, e := tracker.GetIssue(ctx, issue)
					if e == nil {
						detail = d
					}
					return e
				}, func() *ui.Card {
					return buildWorkspaceStoryCard(detail)
				})
				if err != nil {
					return fmt.Errorf("fetching issue: %w", err)
				}
			}

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

			// Interactive repository selection — only for fresh
			// workspaces. Reusing an existing one shouldn't re-ask the
			// user to pick repos; the per-repo assess will simply
			// no-op anything that's already there and apply anything
			// that's been added to config since.
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

			// Per-repo branch actions, then worktree actions (order matters:
			// branches must exist before worktrees can be created).
			for _, r := range repositories {
				repoPath := r.Path
				repoName := r.Name

				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					Action: "branch",
					Type:   "repo",
					Name:   repoName,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						exists, err := g.BranchExists(ctx, repoPath, branchName)
						if err != nil {
							return 0, "", err
						}
						if exists {
							return ActionCompleted, branchName, nil
						}
						return ActionNeeded, branchName, nil
					},
					Apply: func(ctx context.Context) error {
						if fromHead {
							return g.CreateBranchFromHead(ctx, repoPath, branchName)
						}
						return g.CreateBranch(ctx, repoPath, branchName)
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
