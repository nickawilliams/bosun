package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

			// --- Resolve ---

			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			repositories, err := resolveRepositories(filterRepositories)
			if err != nil {
				return err
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("Code host: %v", hostErr))
			}

			// Per-repository resolution.
			g := git.New()
			var actions []Action

			type releaseResult struct {
				repo    string
				release code.Release
				version string
			}
			var releaseResults []releaseResult

			if host != nil {
				for _, r := range repositories {
					branch, err := g.GetCurrentBranch(ctx, r.Path)
					if err != nil {
						ui.Fail(fmt.Sprintf("%s: cannot determine branch: %v", r.Name, err))
						continue
					}

					identity, err := gh.ParseRemote(ctx, r.Path)
					if err != nil {
						ui.Fail(fmt.Sprintf("%s: %v", r.Name, err))
						continue
					}

					owner := identity.Owner
					repoName := identity.Name

					var currentTag string
					if err := ui.RunCard(fmt.Sprintf("Fetching latest tag for %s", r.Name), func() error {
						var e error
						currentTag, e = host.GetLatestTag(ctx, owner, repoName)
						return e
					}); err != nil {
						ui.Fail(fmt.Sprintf("%s: %v", r.Name, err))
						continue
					}

					nextVersion, err := code.DeriveNextVersion(currentTag, bump)
					if err != nil {
						ui.Fail(fmt.Sprintf("%s: %v", r.Name, err))
						continue
					}

					from := currentTag
					if from == "" {
						from = "(none)"
					}

					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						Label:  "Create Release",
						Target: r.Name,
						Assess: func(_ context.Context) (ActionState, string, error) {
							if currentTag == nextVersion {
								return ActionCompleted, currentTag, nil
							}
							return ActionNeeded, fmt.Sprintf("%s → %s", from, nextVersion), nil
						},
						Apply: func(ctx context.Context) error {
							rel, err := host.CreateRelease(ctx, code.CreateReleaseRequest{
								Owner:      owner,
								Repository: repoName,
								Tag:        nextVersion,
								Target:     branch,
								Name:       nextVersion,
							})
							if err != nil {
								return err
							}
							releaseResults = append(releaseResults, releaseResult{
								repo: r.Name, release: rel, version: nextVersion,
							})
							return nil
						},
					})
				}
			}

			tracker, _ := newIssueTracker()
			if sa, ok := statusAction(tracker, issue, "", "ready_for_release"); ok {
				actions = append(actions, sa)
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			// Post-apply: notify release channel.
			if len(releaseResults) > 0 {
				items := make([]notify.Item, len(releaseResults))
				for i, r := range releaseResults {
					items[i] = notify.Item{
						Label:  r.repo,
						URL:    r.release.URL,
						Detail: r.version,
					}
				}
				sendNotification(ctx, notify.Message{
					Channel:  viper.GetString("slack.channel_release"),
					IssueKey: issue,
					Items:    items,
				})
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	return cmd
}
