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

type reviewRepoPlan struct {
	repo     Repo
	owner    string
	name     string
	branch   string
	existing code.PullRequest // Number > 0 if PR already exists
}

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

			// --- Resolve ---

			// Repos.
			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Issue details for PR title.
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

			prTitle := buildPRTitle(issue, detail.Title)
			baseBranch := viper.GetString("pull_request.base")
			if baseBranch == "" {
				baseBranch = "main"
			}

			// Code host.
			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Code host: %v", hostErr)).Print()
			}

			// Per-repo resolution: branch, remote, existing PR check.
			g := git.New()
			var repoPlans []reviewRepoPlan

			if host != nil {
				for _, r := range repos {
					branch, err := g.GetCurrentBranch(ctx, r.Path)
					if err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: cannot determine branch: %v", r.Name, err)).Print()
						continue
					}

					identity, err := gh.ParseRemote(ctx, r.Path)
					if err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					// Check for existing PR.
					existing, _ := host.GetPRForBranch(ctx, identity.Owner, identity.Name, branch)

					repoPlans = append(repoPlans, reviewRepoPlan{
						repo:     r,
						owner:    identity.Owner,
						name:     identity.Name,
						branch:   branch,
						existing: existing,
					})
				}
			}

			// --- Plan ---
			plan := ui.NewPlan()
			for _, rp := range repoPlans {
				detail := fmt.Sprintf("%s → %s", rp.branch, baseBranch)
				if rp.existing.Number > 0 {
					plan.Add(ui.PlanNoChange, "Pull Request", rp.repo.Name, fmt.Sprintf("#%d", rp.existing.Number))
				} else {
					plan.Add(ui.PlanCreate, "Create Pull Request", rp.repo.Name, detail)
				}
			}
			if statusName, err := resolveStatus("review"); err == nil {
				plan.Add(ui.PlanModify, "Update Issue Status", issue, fmt.Sprintf("→ %s", statusName))
			}

			if !confirmPlan(cmd, plan) {
				return nil
			}

			// --- Apply ---
			for _, rp := range repoPlans {
				if rp.existing.Number > 0 {
					ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s #%d (exists)", rp.repo.Name, rp.existing.Number)).
						Muted(rp.existing.URL).
						Print()
					continue
				}

				var pr code.PullRequest
				if err := ui.RunCard(fmt.Sprintf("Creating PR for %s", rp.repo.Name), func() error {
					var e error
					pr, e = host.CreatePR(ctx, code.CreatePRRequest{
						Owner: rp.owner,
						Repo:  rp.name,
						Head:  rp.branch,
						Base:  baseBranch,
						Title: prTitle,
					})
					return e
				}); err != nil {
					ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", rp.repo.Name, err)).Print()
					continue
				}

				ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s #%d", rp.repo.Name, pr.Number)).
					Muted(pr.URL).
					Print()
			}

			// TODO: Notify (phase 5)

			if trackerErr == nil {
				transitionIssueStatus(ctx, issue, "in_progress", "review")
			}
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	return cmd
}
