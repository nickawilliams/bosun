package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		Annotations: map[string]string{
			headerAnnotationTitle: "deploy preview",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			if err := cc.RequireIssue(); err != nil {
				return err
			}
			issueKey := cc.Issue

			ctx := cmd.Context()
			detail := emitLifecyclePreamble(ctx, issueKey)
			currentStatus := detail.Status
			issueTitle := detail.Title
			issueType := detail.Type
			issueURL := detail.URL

			provider, err := newPreviewProvider(cc.Workspace)
			if err != nil {
				return fmt.Errorf("preview provider: %w", err)
			}

			// Separate pipeline check drives whether the deploy/teardown
			// actions are even built. The provider has its own pipeline
			// reference internally; this is just the pre-plan gate.
			_, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}

			const stage = "preview"
			force, _ := cmd.Flags().GetBool("force")

			resolution, err := resolvePreview(cmd, ctx, provider, issueKey, stage, force)
			if err != nil {
				return err
			}

			if resolution.previewName != "" {
				ui.Selected("preview", resolution.previewName)
			}

			// --- Plan + Apply ---

			var actions []Action
			var prData []repoPR

			if resolution.teardownName != "" && pipelineErr == nil {
				actions = append(actions, buildTeardownAction(provider, resolution.teardownName, issueKey))
			}

			if resolution.isCurrent {
				actions = append(actions, currentAction(resolution.previewName))
			}

			if resolution.isAdopt {
				actions = append(actions, adoptAction(provider, issueKey, resolution.previewName))
			}

			if resolution.deployName != "" && pipelineErr == nil {
				deployAction, prs := buildDeployAction(cmd, ctx, cc.Workspace, provider, issueKey, resolution)
				actions = append(actions, deployAction)
				actions = append(actions, prDetailActions(prs)...)
				prData = prs
			}

			tracker, _ := newIssueTracker()
			if sa, ok := statusAction(tracker, issueKey, currentStatus, "preview"); ok {
				actions = append(actions, sa)
			}

			channel := viper.GetString("notification.channel_review")
			previewNotifier, previewNotifierErr := newNotifier()
			if previewNotifierErr == nil {
				defer previewNotifier.Close()
			}
			if channel != "" && previewNotifierErr == nil {
				actions = append(actions, Action{
					Op:     ui.PlanModify,
					Action: "notify",
					Type:   "channel",
					Name:   channel,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						ref, _ := previewNotifier.FindThread(ctx, channel, issueKey)
						if ref.Timestamp == "" {
							return ActionSkipped, "", nil
						}
						return ActionNeeded, "update review notification", nil
					},
					Apply: func(ctx context.Context) error {
						// Build notification items from PR data.
						items := make([]notify.Item, len(prData))
						for i, rp := range prData {
							items[i] = notify.Item{
								Label:     rp.RepoName,
								URL:       rp.PR.URL,
								Detail:    fmt.Sprintf("#%d", rp.PR.Number),
								Body:      rp.PR.Body,
								BranchURL: fmt.Sprintf("https://github.com/%s/%s/tree/%s", rp.Owner, rp.Repo, rp.Branch),
							}
						}
						// Resolve GitHub avatar for card icons.
						var iconURL string
						if host, err := newCodeHost(); err == nil {
							if user, err := host.GetAuthenticatedUser(ctx); err == nil {
								iconURL = fmt.Sprintf("https://github.com/%s.png?size=36", user)
							}
						}

						_, err := previewNotifier.Notify(ctx, notify.Message{
							Channel:  channel,
							IssueKey: issueKey,
							Content: buildNotifyContent("review", notifyTemplateData{
								IssueKey:    issueKey,
								IssueTitle:  issueTitle,
								IssueType:   issueType,
								IssueURL:    issueURL,
								IconURL:     iconURL,
								PreviewName: resolution.previewName,
								PreviewURL:  resolution.previewURL,
								Items:       items,
							}),
						})
						return err
					},
				})
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			return nil
		},
	}

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().StringSlice("service", nil, "service to deploy (can be repeated; overrides auto-detection)")
	cmd.Flags().String("name", "", "ephemeral environment name (e.g., brave-falcon; auto-generated if not set)")
	cmd.Flags().Bool("force", false, "auto-confirm prompts; replace existing or create missing without asking")
	return cmd
}

// buildTeardownAction returns a single rolled-up teardown action. The
// adapter fans out the underlying workflow dispatches across all
// configured targets.
func buildTeardownAction(provider preview.Provider, name, issueKey string) Action {
	return Action{
		Op:     ui.PlanDestroy,
		Action: "teardown",
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionNeeded, name, nil
		},
		Apply: func(ctx context.Context) error {
			return provider.Destroy(ctx, issueKey, name)
		},
	}
}

// adoptAction returns a no-op plan item that records the existing env in
// the issue tracker without triggering a deploy. The action renders as
// PlanNoChange but Apply still calls provider.Adopt so future
// invocations see the same name. Used when the user is claiming an env
// that wasn't previously tracked.
func adoptAction(provider preview.Provider, issueKey, name string) Action {
	return Action{
		Op:     ui.PlanNoChange,
		Action: "adopt",
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionNeeded, "reachable", nil
		},
		Apply: func(ctx context.Context) error {
			return provider.Adopt(ctx, issueKey, name)
		},
	}
}

// currentAction returns a no-op plan item indicating that the tracked
// preview environment is already alive and matches what would be deployed.
// No metadata work is needed (the stored name already points at this env)
// and no Apply runs.
func currentAction(name string) Action {
	return Action{
		Op:     ui.PlanNoChange,
		Action: "deploy",
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionCompleted, "current", nil
		},
	}
}

// buildDeployAction returns a single rolled-up deploy action plus the
// resolved PR data so callers can reuse it for notifications. The
// adapter fans out the workflow dispatches across all configured
// targets internally.
func buildDeployAction(cmd *cobra.Command, ctx context.Context, workspace string, provider preview.Provider, issueKey string, resolution previewResolution) (Action, []repoPR) {
	services, overrides, prData := resolvePreviewInputs(cmd, ctx, workspace)

	deployOp := ui.PlanCreate
	if resolution.isRedeploy {
		deployOp = ui.PlanModify
	}

	return Action{
		Op:     deployOp,
		Action: "deploy",
		Type:   "env",
		Name:   resolution.previewName,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionNeeded, "preview env", nil
		},
		Apply: func(ctx context.Context) error {
			_, err := provider.Create(ctx, preview.Claim{
				IssueKey:  issueKey,
				Name:      resolution.deployName,
				Services:  services,
				Overrides: overrides,
			})
			return err
		},
	}, prData
}

// prDetailActions renders one PlanDetail row per affected repo's PR
// tag, surfacing the pr-N tag that will be deployed for each service.
func prDetailActions(prs []repoPR) []Action {
	out := make([]Action, 0, len(prs))
	for _, rp := range prs {
		rp := rp
		tag := fmt.Sprintf("pr-%d", rp.PR.Number)
		out = append(out, Action{
			Op:     ui.PlanDetail,
			Action: "deploy",
			Type:   "repo",
			Name:   rp.RepoName,
			Assess: func(_ context.Context) (ActionState, string, error) {
				return ActionNeeded, tag, nil
			},
		})
	}
	return out
}

// resolvePreviewInputs computes the services list, image overrides, and
// PR data for a deploy. Services come from --service when set, otherwise
// from affected-service detection. Overrides always derive from affected
// detection (PR lookups per repo). UI output (printAffectedSummary) fires
// during plan build, matching today's flow.
func resolvePreviewInputs(cmd *cobra.Command, ctx context.Context, workspace string) ([]string, map[string]string, []repoPR) {
	flagServices, _ := cmd.Flags().GetStringSlice("service")

	g := git.New()
	results, _ := resolveAffectedServices(ctx, workspace, g)

	var services []string
	if len(flagServices) > 0 {
		services = flagServices
	} else {
		printAffectedSummary(results)
		for _, r := range results {
			services = append(services, r.Services...)
		}
	}

	overrides, prs, _ := buildImageOverrides(ctx, results)
	return services, overrides, prs
}

// repoPR pairs a repository with its resolved pull request.
type repoPR struct {
	RepoName string
	Branch   string
	Owner    string
	Repo     string
	PR       code.PullRequest
}

// buildImageOverrides looks up the PR number for each affected repo's
// branch and produces a service → "pr-N" override map plus the resolved
// PR data for use in notifications. Returns nil map when no repos have
// changes that resolve to PRs.
func buildImageOverrides(ctx context.Context, results []AffectedResult) (map[string]string, []repoPR, error) {
	var reposWithChanges []AffectedResult
	for _, r := range results {
		if r.HasChanges && len(r.Services) > 0 {
			reposWithChanges = append(reposWithChanges, r)
		}
	}
	if len(reposWithChanges) == 0 {
		return nil, nil, nil
	}

	host, err := newCodeHost()
	if err != nil {
		return nil, nil, fmt.Errorf("code host (needed for image overrides): %w", err)
	}

	overrides := make(map[string]string)
	var prs []repoPR
	for _, r := range reposWithChanges {
		identity, err := gh.ParseRemote(ctx, r.RepoPath)
		if err != nil {
			ui.Fail(fmt.Sprintf("%s: %v", r.RepoName, err))
			continue
		}
		pr, err := host.GetPRForBranch(ctx, identity.Owner, identity.Name, r.Branch)
		if err != nil {
			ui.Fail(fmt.Sprintf("%s: %v", r.RepoName, err))
			continue
		}
		if pr.Number == 0 {
			ui.Skip(fmt.Sprintf("%s: no PR for branch %q, skipping", r.RepoName, r.Branch))
			continue
		}

		tag := fmt.Sprintf("pr-%d", pr.Number)
		for _, svc := range r.Services {
			overrides[svc] = tag
		}

		prs = append(prs, repoPR{
			RepoName: r.RepoName,
			Branch:   r.Branch,
			Owner:    identity.Owner,
			Repo:     identity.Name,
			PR:       pr,
		})

		ui.Complete(fmt.Sprintf("%s: PR #%d → %s", r.RepoName, pr.Number, tag))
	}

	if len(overrides) == 0 {
		return nil, prs, nil
	}
	return overrides, prs, nil
}

