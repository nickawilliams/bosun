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

			// --- Plan + Apply ---
			plan := ui.NewPlan()
			for _, rp := range repoPlans {
				from := rp.currentTag
				if from == "" {
					from = "(none)"
				}
				plan.Add(ui.PlanCreate, "Create Release", rp.repo.Name, fmt.Sprintf("%s → %s", from, rp.nextVersion))
			}
			addStatusPlanItem(plan, issue, "", "ready_for_release")

			// Build actions.
			var actions []PlanAction
			for _, rp := range repoPlans {
				actions = append(actions, func() error {
					_, err := host.CreateRelease(ctx, code.CreateReleaseRequest{
						Owner:  rp.owner,
						Repo:   rp.name,
						Tag:    rp.nextVersion,
						Target: rp.branch,
						Name:   rp.nextVersion,
					})
					return err
				})
			}

			statusName, _ := resolveStatus("ready_for_release")
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil && statusName != "" {
				actions = append(actions, func() error {
					return tracker.SetStatus(ctx, issue, statusName)
				})
			}

			// TODO: Notify release channel (phase 5)

			if err := runPlanCard(cmd, plan, actions); err != nil {
				return err
			}
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	return cmd
}
