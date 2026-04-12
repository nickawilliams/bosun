package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
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

			// --- Fetch phase ---

			var detail issuepkg.Issue
			var trackerErr, fetchErr error

			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				fetchErr = ui.RunCard("Fetching issue", func() error {
					var e error
					detail, e = tracker.GetIssue(ctx, issue)
					return e
				})
			}

			repos, repoErr := resolveRepos(nil)
			var repoStatuses []repoStatus
			if repoErr == nil {
				repoStatuses = collectBranchStatus(ctx, issue, repos)
			}

			// --- Display phase ---

			// Issue details.
			if trackerErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue tracker: %v", trackerErr)).Print()
			} else if fetchErr != nil {
				ui.NewCard(ui.CardFailed, fmt.Sprintf("Issue tracker: %v", fetchErr)).Print()
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

			// Repo branch status.
			if repoErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Repos: %v", repoErr)).Print()
			} else if len(repoStatuses) == 0 {
				ui.NewCard(ui.CardSkipped, "No branches found for "+issue).Print()
			} else {
				var lines []string
				for _, s := range repoStatuses {
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

			// PR status from code host.
			if repoErr == nil && len(repoStatuses) > 0 {
				host, hostErr := newCodeHost()
				if hostErr != nil {
					ui.NewCard(ui.CardSkipped, fmt.Sprintf("Code host: %v", hostErr)).Print()
				} else {
					prStatuses := collectPRStatus(ctx, host, repoStatuses, repos)
					if len(prStatuses) > 0 {
						var lines []string
						for _, ps := range prStatuses {
							line := fmt.Sprintf("%-12s #%-4d %s", ps.repoName, ps.pr.Number, ps.pr.State)
							if ps.pr.Review != "" {
								line += " · " + ps.pr.Review
							}
							lines = append(lines, line)
							lines = append(lines, fmt.Sprintf("%-12s %s", "", ps.pr.URL))
						}
						ui.NewCard(ui.CardInfo, "Pull Requests").
							Text(lines...).
							Print()
					}
				}
			}

			// TODO: CI/CD status (phase 6)

			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}

type repoStatus struct {
	name    string
	branch  string
	exists  bool
	dirty   bool
	current bool
}

// collectBranchStatus checks each repo for branches matching the issue key.
func collectBranchStatus(ctx context.Context, issueKey string, repos []Repo) []repoStatus {
	g := git.New()
	var statuses []repoStatus

	for _, r := range repos {
		currentBranch, err := g.GetCurrentBranch(ctx, r.Path)
		if err != nil {
			continue
		}

		if strings.Contains(currentBranch, issueKey) {
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

	return statuses
}

type prStatus struct {
	repoName string
	pr       code.PullRequest
}

// collectPRStatus checks each repo for PRs matching the branch.
func collectPRStatus(ctx context.Context, host code.Host, repoStatuses []repoStatus, repos []Repo) []prStatus {
	var results []prStatus

	for _, s := range repoStatuses {
		if !s.exists {
			continue
		}

		// Find the repo path to parse the remote.
		var repoPath string
		for _, r := range repos {
			if r.Name == s.name {
				repoPath = r.Path
				break
			}
		}
		if repoPath == "" {
			continue
		}

		identity, err := gh.ParseRemote(ctx, repoPath)
		if err != nil {
			continue
		}

		pr, err := host.GetPRForBranch(ctx, identity.Owner, identity.Name, s.branch)
		if err != nil || pr.Number == 0 {
			continue
		}

		results = append(results, prStatus{repoName: s.name, pr: pr})
	}

	return results
}
