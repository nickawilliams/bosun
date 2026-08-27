package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/code"
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
			detail, _ := emitLifecyclePreamble(ctx, issueKey)
			currentStatus := detail.Status
			issueTitle := detail.Title
			issueType := detail.Type
			issueURL := detail.URL

			provider, err := newPreviewProvider(cc.Workspace)
			if err != nil {
				return fmt.Errorf("preview provider: %w", err)
			}

			const stage = preview.ConfigGroup
			force, _ := cmd.Flags().GetBool("force")

			resolution, err := resolvePreview(cmd, ctx, provider, issueKey, stage, force)
			if err != nil {
				return err
			}

			if resolution.previewName != "" && !resolution.cardPainted {
				ui.Selected("preview", resolution.previewName)
			}

			// --- Plan + Apply ---

			var actions []Action
			var prData []repoPR

			// The provider answers for itself whether each half of its
			// lifecycle is wired. This replaces a CI/CD capability
			// check that gated both rows regardless of whether the
			// selected provider dispatches workflows at all.
			ready := &previewReadiness{provider: provider}

			if resolution.teardownName != "" {
				actions = append(actions, buildTeardownAction(ctx, ready, provider, resolution.teardownName, issueKey))
			}

			if resolution.isCurrent {
				actions = append(actions, currentAction(resolution.previewName))
			}

			if resolution.isAdopt {
				actions = append(actions, adoptAction(provider, issueKey, resolution.previewName))
			}

			if resolution.deployName != "" {
				deployAction, prs, err := buildDeployAction(cmd, ctx, ready, cc.Workspace, provider, issueKey, resolution)
				if err != nil {
					return err
				}
				actions = append(actions, deployAction)
				actions = append(actions, prDetailActions(prs)...)
				prData = prs
			}

			tracker, _ := newIssueTracker()
			if sa, ok := statusAction(tracker, issueKey, currentStatus, "preview"); ok {
				actions = append(actions, sa)
			}

			channel := viper.GetString("notification.channels.review")
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
						// The host supplies both the branch links and the
						// author avatar; without one the notification
						// still goes out, just plainer.
						host, _ := newCodeHost()

						// Build notification items from PR data.
						items := make([]notify.Item, len(prData))
						for i, rp := range prData {
							items[i] = notify.Item{
								Label:     rp.RepoName,
								URL:       rp.PR.URL,
								Detail:    fmt.Sprintf("#%d", rp.PR.Number),
								Body:      rp.PR.Body,
								BranchURL: branchURL(host, rp.Owner, rp.Repo, rp.Branch),
							}
						}
						// Resolve the author's avatar for card icons.
						var iconURL string
						if host != nil {
							if user, err := host.GetAuthenticatedUser(ctx); err == nil {
								iconURL = avatarURL(host, user)
							}
						}

						_, err := previewNotifier.Notify(ctx, notify.Message{
							Channel:  channel,
							IssueKey: issueKey,
							Content: buildNotifyContent("review", notifyTemplateData{
								IssueKey:         issueKey,
								IssueTitle:       issueTitle,
								IssueType:        issueType,
								IssueURL:         issueURL,
								IssueDescription: detail.Description,
								IssueIconURL:     detail.TypeIconURL,
								IconURL:          iconURL,
								PreviewName:      resolution.previewName,
								PreviewURL:       resolution.previewURL,
								Items:            items,
							}),
						})
						return err
					},
				})
			} else if previewNotifierErr == nil {
				// Notification is configured (the notifier built), but this
				// command has no channel to post to — a partial config, not an
				// opt-out. Surface it (naming the key) rather than dropping the
				// announcement silently; an unconfigured provider stays quiet.
				ui.Skip("notification: set notification.channels.review to announce the preview")
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.AddCommand(newPreviewListCmd())

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().StringSlice("service", nil, "service to deploy (can be repeated; overrides auto-detection)")
	cmd.Flags().String("name", "", "ephemeral environment name (e.g., brave-falcon; auto-generated if not set)")
	cmd.Flags().String("default-branch", "", "branch every unpinned service runs (provider default if not set)")
	cmd.Flags().Bool("force", false, "replace existing or create missing without asking; proceed past failed probes (plan confirmation still applies — see --approve)")
	return cmd
}

// buildTeardownAction returns a single rolled-up teardown action. The
// adapter fans out the underlying workflow dispatches across all
// configured targets.
//
// The provider is asked whether it can tear down at all, so a provider
// with no backend for the destroy half says so in its own words,
// instead of the command guessing on its behalf from a capability the
// provider may never touch.
//
// Neither answer aborts the command. A destroy half that is unwired,
// or one whose targets will not resolve, is this row's problem and not
// the deploy's — the halves are independently configured, which is why
// readiness is asked per operation in the first place. A fault becomes
// a ✗ row, which runActions surfaces after letting the siblings run,
// so a typo in preview.down still leaves the new env deployed.
func buildTeardownAction(ctx context.Context, ready *previewReadiness, provider preview.Provider, name, issueKey string) Action {
	reason, err := ready.reason(ctx, preview.OpDestroy)
	switch {
	case err != nil:
		return failedAction(ui.PlanDestroy, "teardown", name, err)
	case reason != "":
		return noopAction("teardown", name, reason)
	}

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
// targets internally. A non-nil error means input resolution was
// aborted (e.g. the user cancelled the service-selection form) and
// the command should stop rather than deploy an empty set.
//
// A provider that cannot deploy — whether because it reports no
// backend or because answering faulted — yields a row and no PR data,
// before any input is resolved. Neither is the returned error: a
// deploy that cannot happen is this row's problem, and aborting here
// would take a teardown queued alongside it down too. See
// previewReadiness.
func buildDeployAction(cmd *cobra.Command, ctx context.Context, ready *previewReadiness, workspace string, provider preview.Provider, issueKey string, resolution previewResolution) (Action, []repoPR, error) {
	reason, err := ready.reason(ctx, preview.OpCreate)
	switch {
	case err != nil:
		return failedAction(ui.PlanCreate, "deploy", resolution.previewName, err), nil, nil
	case reason != "":
		return noopAction("deploy", resolution.previewName, reason), nil, nil
	}

	services, overrides, prData, err := resolvePreviewInputs(cmd, ctx, workspace)
	if err != nil {
		return Action{}, nil, err
	}

	// Empty means "the provider's own default" — the deployment API
	// falls back to main, and the workflow-dispatch adapter has no
	// notion of the concept at all. Only set it when the user asked for
	// something else.
	defaultBranch, _ := cmd.Flags().GetString("default-branch")
	defaultBranch = strings.TrimSpace(defaultBranch)

	// Nothing selected to deploy (full deselection in the form, or no
	// deployable services at all) — render an honest no-op row rather
	// than a "+ deploy env" that would claim an environment with zero
	// services behind it.
	if len(services) == 0 {
		return noopAction("deploy", resolution.previewName, "no services selected"), prData, nil
	}

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
				IssueKey:      issueKey,
				Name:          resolution.deployName,
				Overrides:     overrides,
				DefaultBranch: defaultBranch,
			})
			return err
		},
	}, prData, nil
}

// noopAction renders an honest no-op row carrying the reason nothing
// will happen — nothing selected to deploy, or a provider with no
// backend to act through.
//
// A row stating the reason beats both alternatives: an operative row
// that claims work nothing stands behind, and ActionSkipped, which
// omits the row and takes the reason with it — the silent exit this
// command used to produce when its gate lived upstream.
//
// There is deliberately no op parameter. runActions renders every
// ActionCompleted as ui.PlanNoChange regardless of the action's own
// op, so one would be inert, and a field that looks like it selects a
// glyph but doesn't is worse than its absence.
func noopAction(action, name, reason string) Action {
	return Action{
		Op:     ui.PlanNoChange,
		Action: action,
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionCompleted, reason, nil
		},
	}
}

// failedAction renders a ✗ row for a step whose readiness could not
// even be established — a workflow target that will not parse, say.
//
// It is a row rather than a returned error so the siblings still plan
// and apply: runActions treats a failed assessment as a per-target
// failure, not a run-wide abort, and returns the error afterwards so
// the exit code still reflects it. That contract is what keeps a typo
// in the teardown wiring from also cancelling the deploy queued
// beside it.
func failedAction(op ui.PlanOp, action, name string, err error) Action {
	return Action{
		Op:     op,
		Action: action,
		Type:   "env",
		Name:   name,
		Assess: func(_ context.Context) (ActionState, string, error) {
			return ActionNeeded, "", err
		},
	}
}

// previewReadiness asks a provider whether it can carry out an
// operation and announces each distinct "no backend for that" answer
// once.
//
// It exists because the answer needs to reach the user twice over. The
// plan row carries it for a reader of the plan — but the plan card
// renders only on a terminal, so for a piped or --output run the row
// alone is silence. ui.Skip is the channel that survives both, and it
// is the one this command already used, back when the question was put
// to the CI/CD capability instead of to the provider.
//
// Asking before the rows are built is deliberate on the deploy side:
// resolving deploy inputs detects affected repos, pushes branches, and
// can put a selection form on screen, none of which should happen for
// a deploy the provider has already said it cannot carry out.
type previewReadiness struct {
	provider preview.Provider
	reported map[string]bool
}

// reason returns why the provider cannot carry out op, or "" when it
// can. A non-nil error means answering the question itself failed,
// which is a fault rather than a skip.
func (r *previewReadiness) reason(ctx context.Context, op preview.Operation) (string, error) {
	err := r.provider.Ready(ctx, op)
	switch {
	case err == nil:
		return "", nil
	case !errors.Is(err, preview.ErrNotConfigured):
		return "", err
	}

	// The two halves of a provider's lifecycle usually go unwired
	// together and then answer identically; reporting the same
	// sentence per row would read as two separate problems.
	reason := err.Error()
	if !r.reported[reason] {
		if r.reported == nil {
			r.reported = map[string]bool{}
		}
		r.reported[reason] = true
		ui.Skip(reason)
	}
	return reason, nil
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
// from affected-service detection (selection-adjusted when the user
// toggled the form). Overrides always derive from detection's PR
// lookups, rendered as the "Services" observation section via
// emitDeploymentSources. A non-nil error means the user cancelled the
// selection form — callers must abort rather than deploy an empty set.
func resolvePreviewInputs(cmd *cobra.Command, ctx context.Context, workspace string) ([]string, map[string]string, []repoPR, error) {
	flagServices, _ := cmd.Flags().GetStringSlice("service")

	g := git.New()
	repos, repoBranch, err := prepareAffectedRepos(ctx, workspace, g)
	if err != nil {
		// Pre-flight aborts (declined push, unresolvable workspace)
		// abort the command — review and release already do; silently
		// continuing here produced a plan that "deployed" an env with
		// zero services.
		return nil, nil, nil, err
	}

	results, overrides, prs, err := emitDeploymentSources(ctx, cmd, g, repos, repoBranch, true)
	if err != nil {
		if errors.Is(err, ErrCancelled) {
			return nil, nil, nil, err
		}
		// Detection failures render as ✗ rows in the Services card;
		// interactively the user sees them and can still cancel at
		// the plan gate. Under -y nobody gets that look — a repo
		// silently dropped from the deploy set is exactly what
		// auto-approve must not gloss over, so fail instead.
		if isAutoApprove(cmd) {
			return nil, nil, nil, err
		}
	}

	services := flagServices
	if len(services) == 0 {
		for _, r := range results {
			services = append(services, r.Services...)
		}
	}
	return services, overrides, prs, nil
}

// repoPR pairs a repository with its resolved pull request.
type repoPR struct {
	RepoName string
	Branch   string
	Owner    string
	Repo     string
	PR       code.PullRequest
}
