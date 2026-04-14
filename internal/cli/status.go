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
				ui.Skip(fmt.Sprintf("Issue tracker: %v", trackerErr))
			} else if fetchErr != nil {
				ui.Fail(fmt.Sprintf("Issue tracker: %v", fetchErr))
			} else {
				ui.Details(detail.Key, ui.NewFields(
					"Title", detail.Title,
					"Status", detail.Status,
					"Type", detail.Type,
					"URL", detail.URL,
				))
			}

			// Repo branch status.
			if repoErr != nil {
				ui.Skip(fmt.Sprintf("Repos: %v", repoErr))
			} else if len(repoStatuses) == 0 {
				ui.Skip("No branches found for " + issue)
			} else {
				fields := make(ui.Fields, 0, len(repoStatuses))
				for _, s := range repoStatuses {
					status := "clean"
					if s.dirty {
						status = "dirty"
					}
					if !s.current {
						status = "not checked out"
					}
					fields = append(fields, ui.Field{
						Key:   s.name,
						Value: s.branch + " · " + status,
					})
				}
				ui.Details("Repos", fields)
			}

			// PR status from code host.
			if repoErr == nil && len(repoStatuses) > 0 {
				host, hostErr := newCodeHost()
				if hostErr != nil {
					ui.Skip(fmt.Sprintf("Code host: %v", hostErr))
				} else {
					prStatuses := collectPRStatus(ctx, host, repoStatuses, repos)
					if len(prStatuses) > 0 {
						fields := make(ui.Fields, 0, len(prStatuses))
						for _, ps := range prStatuses {
							value := fmt.Sprintf("#%d %s", ps.pr.Number, ps.pr.State)
							if ps.pr.Review != "" {
								value += " · " + ps.pr.Review
							}
							value += "\n" + ps.pr.URL
							fields = append(fields, ui.Field{
								Key:   ps.repoName,
								Value: value,
							})
						}
						ui.Details("Pull Requests", fields)
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
