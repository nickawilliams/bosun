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
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Print()

			ctx := cmd.Context()
			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			fromHead, _ := cmd.Flags().GetBool("from-head")

			// --- Resolve ---

			// Fetch issue details for branch naming.
			var detail issuepkg.Issue
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				if err := ui.RunCardReplace("Fetching issue", func() error {
					var fetchErr error
					detail, fetchErr = tracker.GetIssue(ctx, issue)
					return fetchErr
				}, func() *ui.Card {
					return ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s: %s", detail.Type, detail.Key)).
						Subtitle(detail.Title)
				}); err != nil {
					return fmt.Errorf("fetching issue: %w", err)
				}
			}

			// Build branch name.
			branchName := issue
			if detail.Key != "" {
				name, err := buildBranchName(detail.Key, detail.Type, detail.Title)
				if err != nil {
					ui.NewCard(ui.CardSkipped, fmt.Sprintf("Branch naming: %v (using %s)", err, issue)).Print()
				} else {
					branchName = name
				}
			}

			// Resolve repos.
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Interactive repo selection.
			if len(repos) > 1 && len(filterRepos) == 0 && isInteractive() {
				opts := make([]huh.Option[string], len(repos))
				for i, r := range repos {
					opts[i] = huh.NewOption(r.Name, r.Name)
				}

				var selected []string
				rewind := ui.NewCard(ui.CardInput, "Repos").PrintRewindable()
				if err := runForm(
					huh.NewMultiSelect[string]().
						Options(opts...).
						Value(&selected),
				); err != nil {
					rewind()
					return err
				}
				rewind()

				if len(selected) == 0 {
					ui.NewCard(ui.CardSkipped, "No repos selected").Print()
					return nil
				}

				repos, err = resolveRepos(selected)
				if err != nil {
					return err
				}
			}

			// Compute workspace root for worktree path display.
			projectRoot := config.FindProjectRoot()
			wsRoot := viper.GetString("workspace_root")
			if wsRoot == "" && projectRoot != "" {
				wsRoot = projectRoot
			}
			if !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}

			// --- Plan + Apply ---
			cwd, _ := os.Getwd()
			plan := ui.NewPlan()
			for _, r := range repos {
				plan.Add(ui.PlanCreate, "Create Branch", r.Name, branchName)
				wtPath := filepath.Join(wsRoot, branchName, r.Name)
				if rel, err := filepath.Rel(cwd, wtPath); err == nil {
					wtPath = rel
				}
				plan.Add(ui.PlanCreate, "Create Worktree", r.Name, wtPath)
			}
			addStatusPlanItem(plan, issue, detail.Status, "in_progress")

			// Resolve status name for the action.
			statusName, _ := resolveStatus("in_progress")

			// Build actions list.
			wsRepos := cliReposToWorkspaceRepos(repos)
			actions := []PlanAction{
				func() error {
					mgr, err := newWorkspaceManager()
					if err != nil {
						return err
					}
					return mgr.Create(context.Background(), branchName, wsRepos, fromHead)
				},
			}

			// Add status transition action if tracker is available.
			if trackerErr == nil && statusName != "" {
				actions = append(actions, func() error {
					return tracker.SetStatus(ctx, issue, statusName)
				})
			}

			if err := runPlanCard(cmd, plan, actions); err != nil {
				return err
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
