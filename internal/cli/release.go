package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Deploy to production",
		Annotations: map[string]string{
			headerAnnotationTitle: "release to production",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			if err := cc.RequireIssue(); err != nil {
				return err
			}
			issue := cc.Issue

			ctx := cmd.Context()

			// --- Pre-flight: migration confirmation ---
			migrationsDone, _ := cmd.Flags().GetBool("migrations-done")
			if !migrationsDone {
				if !isInteractive() {
					return fmt.Errorf("use --migrations-done to confirm migrations have been run")
				}
				confirmed, err := NewDialog("Database Migrations").
					Description("Have any required database migrations been run?").
					Default(false).
					Show()
				if err != nil {
					return err
				}
				if !confirmed {
					ui.Skip("run migrations first, then use --migrations-done")
					return nil
				}
				ui.Complete("migrations confirmed")
			} else {
				ui.Saved("migrations confirmed", "--migrations-done")
			}

			// --- Resolve ---

			detail, _ := emitLifecyclePreamble(ctx, issue)
			currentStatus := detail.Status

			pipeline, pipelineErr := newCICD()
			if pipelineErr != nil {
				ui.Skip(fmt.Sprintf("CI/CD: %v", pipelineErr))
			}
			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("code host: %v", hostErr))
			}

			var actions []Action
			var states []releaseServiceTarget

			// --- Observe: per-service deploy classification ---
			if pipeline != nil && host != nil {
				targets, err := resolveReleaseDeployTargets(ctx, cc.Workspace)
				if err != nil {
					return err
				}
				// --service narrows to the named services (default: all).
				// A name matching no configured target is a hard error:
				// silently filtering it out would leave zero targets and
				// read as "nothing to deploy" for what is actually a typo.
				if svc, _ := cmd.Flags().GetStringSlice("service"); len(svc) > 0 {
					want := make(map[string]bool, len(svc))
					for _, s := range svc {
						want[s] = true
					}
					matched := make(map[string]bool, len(svc))
					filtered := targets[:0]
					for _, t := range targets {
						if want[t.Service] {
							matched[t.Service] = true
							filtered = append(filtered, t)
						}
					}
					var unknown []string
					for _, s := range svc {
						if !matched[s] {
							unknown = append(unknown, s)
						}
					}
					if len(unknown) > 0 {
						return fmt.Errorf("--service %s doesn't match any configured deploy target", strings.Join(unknown, ", "))
					}
					targets = filtered
				}

				if len(targets) == 0 {
					ui.Skip("no production deploy targets configured")
				} else {
					// Observe → Select → record: gather + gate under a
					// spinner, offer the deployable targets as a
					// pre-checked multi-select, record the outcome.
					states, err = selectServiceDeploys(ctx, cmd, host, cc.Workspace, targets)
					if err != nil {
						return err
					}

					// Plan granularity: selected services and errors
					// itemize; unselected services don't — they're the
					// opt-in default, not a decision worth a row each.
					// Accounting happens at the REPO level instead: a
					// repo shipping nothing collapses to one "=" row
					// with its reason (not selected / already live /
					// gate block).
					versionInput := releaseVersionInput()
					repoHasRow := make(map[string]bool)
					for i := range states {
						st := states[i]
						switch {
						case st.err != nil:
							repoHasRow[st.target.RepoName] = true
							actions = append(actions, Action{
								Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: st.target.Label,
								Assess: func(_ context.Context) (ActionState, string, error) {
									return 0, "", st.err
								},
							})
						case st.state == deployGo && st.include:
							repoHasRow[st.target.RepoName] = true
							actions = append(actions, Action{
								Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: st.target.Label,
								Assess: func(_ context.Context) (ActionState, string, error) {
									return ActionNeeded, st.reason, nil
								},
								Apply: func(ctx context.Context) error {
									// Dispatch ON the deploy tag (Ref) with the
									// version input set to it, so the run — and
									// the GitHub Deployment it records — is a
									// deploy of exactly that tag.
									return pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
										Owner:      st.target.Owner,
										Repository: st.target.Repo,
										Workflow:   st.target.Workflow,
										Ref:        st.deployTag,
										Inputs:     map[string]string{versionInput: st.deployTag},
									})
								},
							})
						}
					}
					// Repo-level no-op rows, in first-appearance order.
					seenRepo := make(map[string]bool)
					for i := range states {
						st := states[i]
						repo := st.target.RepoName
						if repoHasRow[repo] || seenRepo[repo] {
							continue
						}
						seenRepo[repo] = true
						detail := repoNoopDetail(states, repo)
						actions = append(actions, Action{
							Op: ui.PlanCreate, Action: "deploy", Type: "repo", Name: repo,
							Assess: func(_ context.Context) (ActionState, string, error) {
								return ActionCompleted, detail, nil
							},
						})
					}
				}
			}

			// --- Status transition ---
			observed := pipeline != nil && host != nil
			if advanceReleaseStatus(observed, states) {
				tracker, _ := newIssueTracker()
				if sa, ok := statusAction(tracker, issue, currentStatus, "done"); ok {
					actions = append(actions, sa)
				}
			}

			return runActions(cmd, ctx, actions)
		},
	}

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	cmd.Flags().Bool("migrations-done", false, "skip migration confirmation")
	cmd.Flags().StringSlice("service", nil, "service to deploy (repeatable; opt-in — without it a non-prompting run deploys nothing)")
	cmd.Flags().String("tag", "", "release tag to deploy (default: the workspace's own release)")
	return cmd
}
