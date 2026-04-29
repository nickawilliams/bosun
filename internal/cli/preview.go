package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
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
			headerAnnotationTitle: "deploy",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issueKey, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Print()

			ctx := cmd.Context()
			tracker, _ := newIssueTracker()
			var currentStatus, issueTitle, issueType, issueURL string
			if tracker != nil {
				if detail, err := fetchIssue(ctx, tracker, issueKey); err != nil {
					ui.Fail(fmt.Sprintf("fetching issue: %v", err))
				} else {
					currentStatus = detail.Status
					issueTitle = detail.Title
					issueType = detail.Type
					issueURL = detail.URL
				}
			}

			const stage = "preview"
			force, _ := cmd.Flags().GetBool("force")

			resolution, err := resolvePreview(cmd, ctx, tracker, issueKey, stage, force)
			if err != nil {
				return err
			}

			if resolution.previewName != "" {
				ui.NewCard(ui.CardSuccess, "preview").Value(resolution.previewName).Print()
			}

			// --- Plan + Apply ---

			var actions []Action
			var prData []repoPR

			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}

			if resolution.teardownName != "" {
				teardownActions := buildTeardownActions(ctx, pipeline, resolution.teardownName, issueKey)
				actions = append(actions, teardownActions...)
			}

			if resolution.isCurrent {
				actions = append(actions, currentAction(resolution.previewName))
			}

			if resolution.isAdopt {
				actions = append(actions, adoptAction(tracker, issueKey, resolution.previewName))
			}

			if resolution.deployName != "" && pipeline != nil {
				deployActions, prs := buildDeployActions(cmd, ctx, pipeline, tracker, issueKey, resolution)
				actions = append(actions, deployActions...)
				prData = prs
			}

			if sa, ok := statusAction(tracker, issueKey, currentStatus, "preview"); ok {
				actions = append(actions, sa)
			}

			channel := viper.GetString("slack.channel_review")
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

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("service", nil, "service to deploy (can be repeated; overrides auto-detection)")
	cmd.Flags().String("name", "", "ephemeral environment name (e.g., brave-falcon; auto-generated if not set)")
	cmd.Flags().Bool("force", false, "auto-confirm prompts; replace existing or create missing without asking")
	return cmd
}

// buildTeardownActions returns the actions for tearing down a preview env by
// name. When no preview.down workflow is configured, prints a one-line skip
// notice and returns an empty slice — stale metadata is still cleaned up
// during resolution, the env just doesn't get an explicit teardown trigger.
func buildTeardownActions(ctx context.Context, pipeline cicd.CICD, name, issueKey string) []Action {
	const stage = "preview.down"
	if strings.TrimSpace(name) == "" {
		ui.Fail("preview down: refusing to trigger without an env name")
		return nil
	}
	targets, err := resolveWorkflowTargets(ctx, stage)
	if err != nil || len(targets) == 0 {
		ui.Skip("preview down: no workflow configured")
		return nil
	}
	if pipeline == nil {
		ui.Skip("preview down: CI/CD not available")
		return nil
	}

	nameKey := stageInputName(stage, "name")
	if nameKey == "" {
		// Without a configured name input, the workflow would be invoked
		// with no name — which many teardown workflows interpret as "clean
		// everything." Refuse rather than risk it.
		ui.Fail("preview down: github_actions.workflows.preview.down.inputs.name is not configured")
		return nil
	}
	issueInputKey := stageInputName(stage, "issue")

	actions := make([]Action, 0, len(targets))
	for _, t := range targets {
		target := t
		actions = append(actions, Action{
			Op:     ui.PlanDestroy,
			Action: "teardown",
			Type:   "repo",
			Name:   target.Label,
			Assess: func(_ context.Context) (ActionState, string, error) {
				return ActionNeeded, name, nil
			},
			Apply: func(ctx context.Context) error {
				if strings.TrimSpace(name) == "" {
					return fmt.Errorf("preview down: refusing to trigger without an env name")
				}
				inputs := map[string]string{
					nameKey: name,
				}
				if issueInputKey != "" {
					inputs[issueInputKey] = issueKey
				}
				return pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
					Owner:      target.Owner,
					Repository: target.Repo,
					Workflow:   target.Workflow,
					Ref:        "main",
					Inputs:     inputs,
				})
			},
		})
	}
	return actions
}

// adoptAction returns a no-op plan item that records the existing env in the
// issue tracker without triggering a deploy. The action renders as
// PlanNoChange but Apply still runs the metadata write so future invocations
// see the same name. Used when the user is claiming an env that wasn't
// previously tracked.
func adoptAction(tracker issue.Tracker, issueKey, name string) Action {
	return Action{
		Op:     ui.PlanNoChange,
		Action: "adopt",
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionNeeded, "reachable", nil
		},
		Apply: func(ctx context.Context) error {
			if tracker == nil {
				return nil
			}
			return tracker.SetProperty(ctx, issueKey, map[string]string{
				"preview_name": name,
			})
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

// buildDeployActions returns the actions for triggering a preview deploy.
// Returns the resolved PR data so callers can reuse it for notifications.
func buildDeployActions(cmd *cobra.Command, ctx context.Context, pipeline cicd.CICD, tracker issue.Tracker, issueKey string, resolution previewResolution) ([]Action, []repoPR) {
	const stage = "preview.up"
	targets, err := resolveWorkflowTargets(ctx, stage)
	if err != nil {
		ui.Fail(fmt.Sprintf("preview up: %v", err))
		return nil, nil
	}
	if len(targets) == 0 {
		ui.Skip("preview up: no workflow configured")
		return nil, nil
	}
	inputs, _ := buildWorkflowInputs(cmd, ctx, stage, issueKey)

	if nameKey := stageInputName(stage, "name"); nameKey != "" {
		inputs[nameKey] = resolution.deployName
	}

	g := git.New()
	results, _ := resolveAffectedServices(ctx, g)
	overridesJSON, prData, _ := buildImageOverrides(ctx, results)
	if overridesJSON != "" {
		inputs["image-overrides"] = overridesJSON
	}

	deployOp := ui.PlanCreate
	if resolution.isRedeploy {
		deployOp = ui.PlanModify
	}

	var actions []Action
	for _, t := range targets {
		target := t
		actions = append(actions, Action{
			Op:     deployOp,
			Action: "deploy",
			Type:   "repo",
			Name:   target.Label,
			Assess: func(_ context.Context) (ActionState, string, error) {
				return ActionNeeded, fmt.Sprintf("main → %s", target.Workflow), nil
			},
			Apply: func(ctx context.Context) error {
				if err := pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
					Owner:      target.Owner,
					Repository: target.Repo,
					Workflow:   target.Workflow,
					Ref:        "main",
					Inputs:     inputs,
				}); err != nil {
					return err
				}
				if tracker != nil && resolution.previewName != "" {
					_ = tracker.SetProperty(ctx, issueKey, map[string]string{
						"preview_name": resolution.previewName,
					})
				}
				return nil
			},
		})

		for _, rp := range prData {
			tag := fmt.Sprintf("pr-%d", rp.PR.Number)
			actions = append(actions, Action{
				Op:     ui.PlanDetail,
				Action: "deploy",
				Type:   "repo",
				Name:   rp.RepoName,
				Assess: func(_ context.Context) (ActionState, string, error) {
					return ActionNeeded, tag, nil
				},
			})
		}
	}
	return actions, prData
}

// buildWorkflowInputs constructs the inputs map for a workflow dispatch.
// Reads input parameter names from the stage's config
// (github_actions.workflows.<stage>.inputs.*).
func buildWorkflowInputs(cmd *cobra.Command, ctx context.Context, stage, issue string) (map[string]string, error) {
	inputs := make(map[string]string)

	if issueKey := stageInputName(stage, "issue"); issueKey != "" {
		inputs[issueKey] = issue
	}

	inputName := stageInputName(stage, "services")
	if inputName == "" {
		return inputs, nil
	}

	// --service flag overrides auto-detection.
	flagServices, _ := cmd.Flags().GetStringSlice("service")
	if len(flagServices) > 0 {
		inputs[inputName] = strings.Join(flagServices, ",")
		return inputs, nil
	}

	// Change-based detection: diff branches, filter to affected services.
	g := git.New()
	results, err := resolveAffectedServices(ctx, g)
	if err != nil {
		return nil, err
	}

	printAffectedSummary(results)

	var affected []string
	for _, r := range results {
		affected = append(affected, r.Services...)
	}
	if len(affected) > 0 {
		inputs[inputName] = strings.Join(affected, ",")
	}

	return inputs, nil
}

// repoPR pairs a repository with its resolved pull request.
type repoPR struct {
	RepoName string
	Branch   string
	Owner    string
	Repo     string
	PR       code.PullRequest
}

// buildImageOverrides constructs the image-overrides JSON input by looking up
// the PR number for each affected repo's branch. Also returns the resolved
// PR data for use in notifications. Format: {"service":"pr-123"}.
func buildImageOverrides(ctx context.Context, results []AffectedResult) (string, []repoPR, error) {
	// Collect repos that have affected services.
	var reposWithChanges []AffectedResult
	for _, r := range results {
		if r.HasChanges && len(r.Services) > 0 {
			reposWithChanges = append(reposWithChanges, r)
		}
	}
	if len(reposWithChanges) == 0 {
		return "", nil, nil
	}

	host, err := newCodeHost()
	if err != nil {
		return "", nil, fmt.Errorf("code host (needed for image overrides): %w", err)
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
		return "", prs, nil
	}

	b, err := json.Marshal(overrides)
	if err != nil {
		return "", prs, fmt.Errorf("marshaling image overrides: %w", err)
	}
	return string(b), prs, nil
}

