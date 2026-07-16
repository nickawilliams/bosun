package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
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

			detail := emitLifecyclePreamble(ctx, issue)
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
				if svc, _ := cmd.Flags().GetStringSlice("service"); len(svc) > 0 {
					want := make(map[string]bool, len(svc))
					for _, s := range svc {
						want[s] = true
					}
					filtered := targets[:0]
					for _, t := range targets {
						if want[t.Service] {
							filtered = append(filtered, t)
						}
					}
					targets = filtered
				}

				if len(targets) == 0 {
					ui.Skip("no production deploy targets configured")
				} else {
					g := git.New()
					_, err := ui.RunCardSteps([]ui.CardStep{{
						Card: ui.NewCard(ui.CardRunning, "deploy").
							Raw("Checking deployed versions..."),
						Run: func() error {
							s, e := resolveServiceDeployTargets(ctx, g, host, cc.Workspace, targets)
							states = s
							return e
						},
					}}, func() *ui.Card {
						return buildDeployTargetsCard(states)
					})
					if err != nil {
						return err
					}

					versionInput := releaseVersionInput()
					for i := range states {
						st := states[i]
						switch {
						case st.err != nil:
							actions = append(actions, Action{
								Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: st.target.Label,
								Assess: func(_ context.Context) (ActionState, string, error) {
									return 0, "", st.err
								},
							})
						case st.state == deployGo:
							actions = append(actions, Action{
								Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: st.target.Label,
								Assess: func(_ context.Context) (ActionState, string, error) {
									return ActionNeeded, st.reason, nil
								},
								Apply: func(ctx context.Context) error {
									// Dispatch ON the tag (Ref: T) with the version
									// input = T, so the run — and the GitHub
									// Deployment it records — is a deploy of T.
									return pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
										Owner:      st.target.Owner,
										Repository: st.target.Repo,
										Workflow:   st.target.Workflow,
										Ref:        st.workTag,
										Inputs:     map[string]string{versionInput: st.workTag},
									})
								},
							})
						default: // deploySkip / deployBlock — explained no-op row
							actions = append(actions, Action{
								Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: st.target.Label,
								Assess: func(_ context.Context) (ActionState, string, error) {
									return ActionCompleted, st.reason, nil
								},
							})
						}
					}
				}
			}

			// --- Status transition ---
			// Advance to done unless the work isn't actually reaching
			// production: something is blocked (no release contains it) and
			// nothing else deploys. All-already-live still advances.
			anyDeploy, anyBlock := false, false
			for _, st := range states {
				switch st.state {
				case deployGo:
					anyDeploy = true
				case deployBlock:
					anyBlock = true
				}
			}
			if !(anyBlock && !anyDeploy) {
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
	cmd.Flags().StringSlice("service", nil, "service to deploy (repeatable; default: all configured services)")
	return cmd
}
