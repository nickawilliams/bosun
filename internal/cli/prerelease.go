package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
)

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

			// Resolve repos.
			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Resolve code host.
			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.NewCard(ui.CardSkipped, fmt.Sprintf("Code host: %v", hostErr)).Print()
			}

			g := git.New()

			// Create releases per repo.
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

					// Get latest tag and derive next version.
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

					fromTag := currentTag
					if fromTag == "" {
						fromTag = "(none)"
					}

					if isDryRun(cmd) {
						ui.NewCard(ui.CardInfo, fmt.Sprintf("Would release %s for %s", nextVersion, r.Name)).
							Subtitle("dry-run").
							KV(
								"Repo", fmt.Sprintf("%s/%s", identity.Owner, identity.Name),
								"Current", fromTag,
								"Next", nextVersion,
								"Target", branch,
							).
							Print()
						continue
					}

					var rel code.Release
					if err := ui.RunCard(fmt.Sprintf("Creating release %s for %s", nextVersion, r.Name), func() error {
						var e error
						rel, e = host.CreateRelease(ctx, code.CreateReleaseRequest{
							Owner:  identity.Owner,
							Repo:   identity.Name,
							Tag:    nextVersion,
							Target: branch,
							Name:   nextVersion,
						})
						return e
					}); err != nil {
						ui.NewCard(ui.CardFailed, fmt.Sprintf("%s: %v", r.Name, err)).Print()
						continue
					}

					ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s %s", r.Name, rel.Tag)).
						Muted(rel.URL).
						Print()
				}
			}

			// TODO: Notify release channel (phase 5)

			transitionIssueStatus(ctx, issue, "preview", "ready_for_release", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	return cmd
}
