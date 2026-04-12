package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		Annotations: map[string]string{
			headerAnnotationTitle: "code review",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()

			// Resolve repos.
			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Fetch issue details for PR title.
			var detail issuepkg.Issue
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				if err := ui.RunCard("Fetching issue", func() error {
					var e error
					detail, e = tracker.GetIssue(ctx, issue)
					return e
				}); err != nil {
					ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue details: %v", err)).Print()
				}
			}

			// Build PR title.
			prTitle := buildPRTitle(issue, detail.Title)
			baseBranch := viper.GetString("pull_request.base")
			if baseBranch == "" {
				baseBranch = "main"
			}

			// Resolve code host.
			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Code host: %v", hostErr)).Print()
			}

			g := git.New()

			// Create PRs per repo.
			if host != nil {
				for _, r := range repos {
					// Get current branch.
					branch, err := g.GetCurrentBranch(ctx, r.Path)
					if err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: cannot determine branch: %v", r.Name, err)).Print()
						continue
					}

					// Parse remote to get owner/repo.
					identity, err := gh.ParseRemote(ctx, r.Path)
					if err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					if isDryRun(cmd) {
						ui.NewCard(ui.CardInfo, fmt.Sprintf("Would create PR for %s", r.Name)).
							Subtitle("dry-run").
							KV(
								"Repo", fmt.Sprintf("%s/%s", identity.Owner, identity.Name),
								"Head", branch,
								"Base", baseBranch,
								"Title", prTitle,
							).
							Print()
						continue
					}

					var pr code.PullRequest
					if err := ui.RunCard(fmt.Sprintf("Creating PR for %s", r.Name), func() error {
						var e error
						pr, e = host.CreatePR(ctx, code.CreatePRRequest{
							Owner: identity.Owner,
							Repo:  identity.Name,
							Head:  branch,
							Base:  baseBranch,
							Title: prTitle,
						})
						return e
					}); err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s #%d", r.Name, pr.Number)).
						Muted(pr.URL).
						Print()
				}
			}

			// TODO: Notify (phase 5)

			transitionIssueStatus(ctx, issue, "in_progress", "review", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	return cmd
}
