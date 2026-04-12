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
			headerAnnotationTitle: "Start work",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			fromHead, _ := cmd.Flags().GetBool("from-head")
			ctx := cmd.Context()

			// Fetch issue details to build branch name.
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

			// Build branch name from pattern + issue metadata.
			branchName := issue
			if detail.Key != "" {
				name, err := buildBranchName(detail.Key, detail.Type, detail.Title)
				if err != nil {
					ui.NewCard(ui.CardSkipped, fmt.Sprintf("Branch naming: %v (using %s)", err, issue)).Print()
				} else {
					branchName = name
				}
			}

			if isDryRun(cmd) {
				kvArgs := []string{"Branch", branchName, "Repos", repoNames(repos)}
				if statusName, err := resolveStatus("in_progress"); err == nil {
					kvArgs = append(kvArgs, "Status", fmt.Sprintf("→ %s", statusName))
				}
				ui.NewCard(ui.CardInfo, "Would create workspace").
					Subtitle("dry-run").
					KV(kvArgs...).
					Print()
				return nil
			}

			// When no --repo filter and multiple repos, let user pick.
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

				// Re-filter repos to just the selected ones.
				repos, err = resolveRepos(selected)
				if err != nil {
					return err
				}
			}

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

			// Transition issue status (graceful — warn on error, don't fail).
			if trackerErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue tracker not configured: %v", trackerErr)).Print()
			} else {
				statusName, err := resolveStatus("in_progress")
				if err != nil {
					ui.NewCard(ui.CardSkipped, fmt.Sprintf("Status mapping: %v", err)).Print()
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "ready"); err != nil {
						return err
					}
					if err := ui.RunCard(fmt.Sprintf("Setting status to %s", statusName), func() error {
						return tracker.SetStatus(ctx, issue, statusName)
					}); err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("Set status: %v", err)).Print()
					}
				}
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
