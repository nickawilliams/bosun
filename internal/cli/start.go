package cli

import (
	"context"
	"fmt"

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

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			branchName := issue
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				ui.DryRun("Would start work on %s", issue)
				ui.Item("Branch:", branchName)
				ui.Item("Repos:", repoNames(repos))
				if statusName, err := resolveStatus("in_progress"); err == nil {
					ui.Item("Status:", fmt.Sprintf("→ %s", statusName))
				}
				return nil
			}

			// Confirm when operating on multiple unfiltered repos.
			if len(repos) > 1 && len(filterRepos) == 0 {
				label := fmt.Sprintf("Start %s in %d repos (%s)?",
					issue, len(repos), repoNames(repos))
				if !promptConfirm(label, true) {
					ui.Warning("Aborted.")
					return nil
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

			ui.Success("Created workspace for %s", issue)
			for _, r := range repos {
				ui.Item(r.Name, r.Path)
			}

			// Transition issue status (graceful — warn on error, don't fail).
			ctx := cmd.Context()
			tracker, trackerErr := newIssueTracker()
			if trackerErr != nil {
				ui.Warning("Issue tracker not configured: %v", trackerErr)
			} else {
				statusName, err := resolveStatus("in_progress")
				if err != nil {
					ui.Warning("Status mapping: %v", err)
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "ready"); err != nil {
						return err
					}
					err = ui.WithSpinner(fmt.Sprintf("Setting status to %s...", statusName), func() error {
						return tracker.SetStatus(ctx, issue, statusName)
					})
					if err != nil {
						ui.Warning("Failed to set status: %v", err)
					} else {
						ui.Success("Set %s to %s", issue, statusName)
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
