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
			rootCard(cmd).Print()
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
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

			// Resolve slug for branch naming.
			slugOverride, _ := cmd.Flags().GetString("slug")
			var slug string
			if detail.Key != "" {
				switch {
				case slugOverride != "":
					slug = slugify(slugOverride)
				case isInteractive():
					slug = slugify(detail.Title)
					slugSlot := ui.NewSlot()
					slugSlot.Show(ui.NewCard(ui.CardInput, "Branch slug").Tight())
					if err := runForm(
						huh.NewInput().
							Title("Slug").
							Value(&slug),
					); err != nil {
						return err
					}
					slugSlot.Clear()
					if slug != "" {
						slug = slugify(slug)
					}
					ui.Complete(fmt.Sprintf("Branch slug: %s", slug))
				}
			}

			// Build branch name.
			branchName := issue
			if detail.Key != "" {
				name, err := buildBranchName(detail.Key, detail.Type, detail.Title, slug)
				if err != nil {
					ui.Skip(fmt.Sprintf("Branch naming: %v (using %s)", err, issue))
				} else {
					branchName = name
				}
			}

			// Resolve repositories.
			repositories, err := resolveRepositories(filterRepositories)
			if err != nil {
				return err
			}

			// Interactive repository selection.
			if len(repositories) > 1 && len(filterRepositories) == 0 && isInteractive() {
				opts := make([]huh.Option[string], len(repositories))
				for i, r := range repositories {
					opts[i] = huh.NewOption(r.Name, r.Name)
				}

				var selected []string
				repositorySlot := ui.NewSlot()
				repositorySlot.Show(ui.NewCard(ui.CardInput, "Repositories").Tight())
				if err := runForm(
					huh.NewMultiSelect[string]().
						Options(opts...).
						Value(&selected),
				); err != nil {
					return err
				}
				repositorySlot.Clear()

				if len(selected) == 0 {
					ui.Skip("No repositories selected")
					return nil
				}

				ui.CompleteWithDetail("Repositories", selected)

				repositories, err = resolveRepositories(selected)
				if err != nil {
					return err
				}
			}

			// Compute workspace root for worktree path display.
			projectRoot := config.FindProjectRoot()
			wsRoot := viper.GetString("workspace_root")
			if !filepath.IsAbs(wsRoot) && projectRoot != "" {
				wsRoot = filepath.Join(projectRoot, wsRoot)
			}

			// --- Plan + Apply ---
			cwd, _ := os.Getwd()
			plan := ui.NewPlan()
			for _, r := range repositories {
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
			wsRepos := cliRepositoriesToWorkspaceRepositories(repositories)
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
	cmd.Flags().String("slug", "", "custom slug for branch name")
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
