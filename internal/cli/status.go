package cli

import (
	"context"
	"fmt"
	"strings"

	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show issue lifecycle status",
		Annotations: map[string]string{
			headerAnnotationTitle: "issue status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()

			// Issue tracker details.
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				var detail issuepkg.Issue
				if err := ui.RunCard("Fetching issue", func() error {
					var fetchErr error
					detail, fetchErr = tracker.GetIssue(ctx, issue)
					return fetchErr
				}); err != nil {
					ui.NewCard(ui.CardFailed, fmt.Sprintf("Issue tracker: %v", err)).Print()
				} else {
					ui.NewCard(ui.CardInfo, detail.Key).
						KV(
							"Title", detail.Title,
							"Status", detail.Status,
							"Type", detail.Type,
							"URL", detail.URL,
						).
						Print()
				}
			} else {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue tracker: %v", trackerErr)).Print()
			}

			// VCS branch status per repo.
			repos, repoErr := resolveRepos(nil)
			if repoErr == nil {
				showBranchStatus(ctx, issue, repos)
			} else {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Repos: %v", repoErr)).Print()
			}

			// TODO: Code host PR status (phase 4)
			// TODO: CI/CD status (phase 6)

			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}

// showBranchStatus checks each repo for branches matching the issue key
// and displays their status.
func showBranchStatus(ctx context.Context, issueKey string, repos []Repo) {
	g := git.New()

	type repoStatus struct {
		name    string
		branch  string
		exists  bool
		dirty   bool
		current bool // this branch is currently checked out
	}

	var statuses []repoStatus

	for _, r := range repos {
		// Check current branch.
		currentBranch, err := g.GetCurrentBranch(ctx, r.Path)
		if err != nil {
			continue
		}

		// Check if any branch contains the issue key.
		matchesCurrent := strings.Contains(currentBranch, issueKey)

		if matchesCurrent {
			dirty, _ := g.IsDirty(ctx, r.Path)
			statuses = append(statuses, repoStatus{
				name:    r.Name,
				branch:  currentBranch,
				exists:  true,
				dirty:   dirty,
				current: true,
			})
			continue
		}

		// Not on a matching branch — check if one exists.
		// Try the raw issue key as a branch name first.
		exists, _ := g.BranchExists(ctx, r.Path, issueKey)
		if exists {
			statuses = append(statuses, repoStatus{
				name:    r.Name,
				branch:  issueKey,
				exists:  true,
				current: false,
			})
		}
	}

	if len(statuses) == 0 {
		ui.NewCard(ui.CardSkipped, "No branches found for "+issueKey).Print()
		return
	}

	var lines []string
	for _, s := range statuses {
		status := "clean"
		if s.dirty {
			status = "dirty"
		}
		if !s.current {
			status = "not checked out"
		}
		lines = append(lines, fmt.Sprintf("%-12s %s · %s", s.name, s.branch, status))
	}

	ui.NewCard(ui.CardInfo, "Repos").
		Text(lines...).
		Print()
}
