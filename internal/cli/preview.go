package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
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
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Print()

			ctx := cmd.Context()
			tracker, _ := newIssueTracker()
			var currentStatus, issueTitle, issueType, issueURL string
			if tracker != nil {
				if detail, err := fetchIssue(ctx, tracker, issue); err != nil {
					ui.Fail(fmt.Sprintf("fetching issue: %v", err))
				} else {
					currentStatus = detail.Status
					issueTitle = detail.Title
					issueType = detail.Type
					issueURL = detail.URL
				}
			}

			// --- Plan + Apply ---

			var actions []Action

			const stage = "preview"

			// Check for existing preview on this issue.
			var existingPreview string
			if tracker != nil {
				if raw, err := tracker.GetProperty(ctx, issue); err == nil && raw != nil {
					var props struct {
						PreviewName string `json:"preview_name"`
					}
					if json.Unmarshal(raw, &props) == nil && props.PreviewName != "" {
						existingPreview = props.PreviewName
					}
				}
			}

			// Resolve preview name: --name flag → existing → interactive prompt → auto-generate.
			var previewName, previewURL string
			isUpdate := false
			if nameKey := stageInputName(stage, "name"); nameKey != "" {
				previewName, _ = cmd.Flags().GetString("name")
				if previewName == "" && existingPreview != "" {
					previewName = existingPreview
					isUpdate = true
				}
				if previewName == "" && forceInteractive(cmd) {
					generated := generateEphemeralName()
					resolved, err := promptDefault("preview name", generated)
					if err != nil {
						return err
					}
					previewName = resolved
				}
				if previewName == "" {
					previewName = generateEphemeralName()
				}
				previewURL = renderStageURL(stage, previewName)
				card := ui.NewCard(ui.CardSuccess, "preview").Value(previewName)
				if isUpdate {
					card.Subtitle("existing")
				}
				card.Print()
			}

			// CI/CD: trigger preview deployment.
			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}
			// Build image overrides and collect PR data for notifications.
			var prData []repoPR
			if pipeline != nil {
				targets, _ := resolveWorkflowTargets(ctx, stage)
				inputs, _ := buildWorkflowInputs(cmd, ctx, stage, issue)

				if nameKey := stageInputName(stage, "name"); nameKey != "" && previewName != "" {
					inputs[nameKey] = previewName
				}

				// Resolve PRs and build image-overrides.
				g := git.New()
				results, _ := resolveAffectedServices(ctx, g)
				overridesJSON, prs, _ := buildImageOverrides(ctx, results)
				prData = prs
				if overridesJSON != "" {
					inputs["image-overrides"] = overridesJSON
				}

				for _, t := range targets {
					target := t
					deployOp := ui.PlanCreate
					deployLabel := "trigger preview deploy"
					if isUpdate {
						deployOp = ui.PlanModify
						deployLabel = "redeploy preview"
					}
					actions = append(actions, Action{
						Op:     deployOp,
						Label:  deployLabel,
						Target: target.Label,
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
							// Persist preview name on the issue for future invocations.
							if tracker != nil && previewName != "" {
								_ = tracker.SetProperty(ctx, issue, map[string]string{
									"preview_name": previewName,
								})
							}
							return nil
						},
					})

					// Per-service detail lines under the workflow trigger.
					for _, rp := range prData {
						tag := fmt.Sprintf("pr-%d", rp.PR.Number)
						actions = append(actions, Action{
							Op:     ui.PlanDetail,
							Label:  "deploy",
							Target: rp.RepoName,
							Assess: func(_ context.Context) (ActionState, string, error) {
								return ActionNeeded, tag, nil
							},
						})
					}
				}
			}

			if sa, ok := statusAction(tracker, issue, currentStatus, "preview"); ok {
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
					Label:  "notify",
					Target: channel,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						ref, _ := previewNotifier.FindThread(ctx, channel, issue)
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
							IssueKey: issue,
							Content: buildNotifyContent("review", notifyTemplateData{
								IssueKey:    issue,
								IssueTitle:  issueTitle,
								IssueType:   issueType,
								IssueURL:    issueURL,
								IconURL:     iconURL,
								PreviewName: previewName,
								PreviewURL:  previewURL,
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
	return cmd
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

