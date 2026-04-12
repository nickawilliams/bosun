package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
)

type prereleaseRepoPlan struct {
	repo        Repo
	owner       string
	name        string
	branch      string
	currentTag  string
	nextVersion string
}

func newPrereleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prerelease",
		Short: "Prepare release artifacts",
		Annotations: map[string]string{
			headerAnnotationTitle: "pre-release",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()
			bump, _ := cmd.Flags().GetString("bump")

			// --- Resolve ---

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Code host: %v", hostErr)).Print()
			}

			g := git.New()
			var repoPlans []prereleaseRepoPlan

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

					// Fetch latest tag (read-only network call).
					var currentTag string
					if err := ui.RunCard(fmt.Sprintf("Fetching latest tag for %s", r.Name), func() error {
						var e error
						currentTag, e = host.GetLatestTag(ctx, identity.Owner, identity.Name)
						return e
					}); err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					nextVersion, err := code.DeriveNextVersion(currentTag, bump)
					if err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					repoPlans = append(repoPlans, prereleaseRepoPlan{
						repo:        r,
						owner:       identity.Owner,
						name:        identity.Name,
						branch:      branch,
						currentTag:  currentTag,
						nextVersion: nextVersion,
					})
				}
			}

			// --- Plan ---
			plan := ui.NewPlan()
			for _, rp := range repoPlans {
				from := rp.currentTag
				if from == "" {
					from = "(none)"
				}
				plan.Add(ui.PlanCreate, "Create Release", rp.repo.Name, fmt.Sprintf("%s → %s", from, rp.nextVersion))
			}
			if statusName, err := resolveStatus("ready_for_release"); err == nil {
				plan.Add(ui.PlanModify, "Update Issue Status", issue, fmt.Sprintf("→ %s", statusName))
			}

			if !confirmPlan(cmd, plan) {
				return nil
			}

			// --- Apply ---
			for _, rp := range repoPlans {
				var rel code.Release
				if err := ui.RunCard(fmt.Sprintf("Creating release %s for %s", rp.nextVersion, rp.repo.Name), func() error {
					var e error
					rel, e = host.CreateRelease(ctx, code.CreateReleaseRequest{
						Owner:  rp.owner,
						Repo:   rp.name,
						Tag:    rp.nextVersion,
						Target: rp.branch,
						Name:   rp.nextVersion,
					})
					return e
				}); err != nil {
					ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", rp.repo.Name, err)).Print()
					continue
				}

				ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s %s", rp.repo.Name, rel.Tag)).
					Muted(rel.URL).
					Print()
			}

			// TODO: Notify release channel (phase 5)

			transitionIssueStatus(ctx, issue, "preview", "ready_for_release", false)
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	return cmd
}
