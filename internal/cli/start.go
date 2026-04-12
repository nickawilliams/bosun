package cli

import (
	"context"
	"fmt"

	"charm.land/huh/v2"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
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
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()
			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			fromHead, _ := cmd.Flags().GetBool("from-head")

			// --- Resolve ---

			// Fetch issue details for branch naming.
			var detail issuepkg.Issue
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				if err := ui.RunCard("Fetching issue", func() error {
					var fetchErr error
					detail, fetchErr = tracker.GetIssue(ctx, issue)
					return fetchErr
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

			// --- Plan ---
			plan := ui.NewPlan()
			for _, r := range repos {
				plan.Add(ui.PlanCreate, "Create Branch", r.Name, branchName)
				plan.Add(ui.PlanCreate, "Create Worktree", r.Name, r.Path)
			}
			addStatusPlanItem(plan, issue, detail.Status, "in_progress")

			if !confirmPlan(cmd, plan) {
				return nil
			}

			// --- Apply ---
			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			if err := ui.RunCard("Creating workspace", func() error {
				return mgr.Create(context.Background(), branchName, wsRepos, fromHead)
			}); err != nil {
				return err
			}

			items := make([]string, len(repos))
			for i, r := range repos {
				items[i] = fmt.Sprintf("%-12s %s", r.Name, r.Path)
			}
			ui.NewCard(ui.CardSuccess, "Created workspace").
				Text(items...).
				Print()

			// Transition issue status (graceful).
			if trackerErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue tracker: %v", trackerErr)).Print()
			} else {
				transitionIssueStatus(ctx, issue, "ready", "in_progress")
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
