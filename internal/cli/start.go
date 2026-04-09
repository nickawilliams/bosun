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
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Header("start", issue)

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
				fetched, err := ui.WithSpinnerResult("Fetching issue...", func() (issuepkg.Issue, error) {
					return tracker.GetIssue(ctx, issue)
				})
				if err != nil {
					return fmt.Errorf("fetching issue: %w", err)
				}
				detail = fetched
				ui.Complete(fmt.Sprintf("Fetched %s: %s", detail.Key, detail.Title))
			}

			// Build branch name from pattern + issue metadata.
			branchName := issue
			if detail.Key != "" {
				name, err := buildBranchName(detail.Key, detail.Type, detail.Title)
				if err != nil {
					ui.Skip(fmt.Sprintf("Branch naming: %v (using %s)", err, issue))
				} else {
					branchName = name
				}
			}

			if isDryRun(cmd) {
				ui.DryRun("Would create workspace")
				kv := ui.NewKV().
					Add("Branch", branchName).
					Add("Repos", repoNames(repos))
				if statusName, err := resolveStatus("in_progress"); err == nil {
					kv.Add("Status", fmt.Sprintf("→ %s", statusName))
				}
				kv.Print()
				return nil
			}

			// When no --repo filter and multiple repos, let user pick.
			if len(repos) > 1 && len(filterRepos) == 0 && isInteractive() {
				const allValue = "*"

				opts := []huh.Option[string]{
					huh.NewOption("All repos", allValue),
				}
				for _, r := range repos {
					opts = append(opts, huh.NewOption(r.Name, r.Name))
				}

				var selected []string
				if err := runForm(
					huh.NewMultiSelect[string]().
						Title("  Repos").
						Options(opts...).
						Value(&selected),
				); err != nil {
					return err
				}

				if len(selected) == 0 {
					ui.Warning("No repos selected.")
					return nil
				}

				// "All repos" selected — use everything.
				allSelected := false
				for _, s := range selected {
					if s == allValue {
						allSelected = true
						break
					}
				}

				if !allSelected {
					repos, err = resolveRepos(selected)
					if err != nil {
						return err
					}
				}
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			err = ui.WithSpinner("Creating workspace...", func() error {
				return mgr.Create(context.Background(), branchName, wsRepos, fromHead)
			})
			if err != nil {
				return err
			}

			items := make([]string, len(repos))
			for i, r := range repos {
				items[i] = fmt.Sprintf("%-12s %s", r.Name, r.Path)
			}
			ui.CompleteWithDetail("Created workspace", items)

			// Transition issue status (graceful — warn on error, don't fail).
			if trackerErr != nil {
				ui.Skip(fmt.Sprintf("Issue tracker not configured: %v", trackerErr))
			} else {
				statusName, err := resolveStatus("in_progress")
				if err != nil {
					ui.Skip(fmt.Sprintf("Status mapping: %v", err))
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "ready"); err != nil {
						return err
					}
					err = ui.WithSpinner(fmt.Sprintf("Setting status to %s...", statusName), func() error {
						return tracker.SetStatus(ctx, issue, statusName)
					})
					if err != nil {
						ui.Fail(fmt.Sprintf("Set status: %v", err))
					} else {
						ui.Complete(fmt.Sprintf("Set status to %s", statusName))
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
