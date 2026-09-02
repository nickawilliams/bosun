package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// TestRunActionsFailedAssessDoesNotAbortSiblings locks the per-target
// error contract: a failed assessment renders as a ✗ plan row while
// the healthy siblings still apply, and the assess error comes back
// after the run so the exit code reflects the partial failure
// (regression: the first Assess error aborted the whole plan before
// anything applied).
func TestRunActionsFailedAssessDoesNotAbortSiblings(t *testing.T) {
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	defer func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	}()

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("approve", true, "")
	cmd.Flags().Bool("dry-run", false, "")

	applied := false
	actions := []Action{
		{
			Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: "bad",
			Assess: func(context.Context) (ActionState, string, error) {
				return 0, "", errors.New("boom")
			},
		},
		{
			Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: "good",
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "ready", nil
			},
			Apply: func(context.Context) error {
				applied = true
				return nil
			},
		},
	}

	err := runActions(cmd, context.Background(), actions)
	if !applied {
		t.Error("healthy sibling was not applied")
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want the assess error returned after the run", err)
	}
}

// TestRunActionsGatedActionSkippedBehindFailure locks the other half
// of that contract: siblings proceeding is the point, but an action
// that speaks for the whole run (RequiresPriorSuccess — the
// issue-tracker transition) must not. Both failure shapes close the
// gate, since both leave the run with work that did not happen.
func TestRunActionsGatedActionSkippedBehindFailure(t *testing.T) {
	tests := []struct {
		name   string
		broken Action
	}{
		{
			name: "apply failure",
			broken: Action{
				Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: "api",
				Assess: func(context.Context) (ActionState, string, error) {
					return ActionNeeded, "ready", nil
				},
				Apply: func(context.Context) error {
					return errors.New("workflow dispatch 422")
				},
			},
		},
		{
			name: "assess failure",
			broken: Action{
				Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: "api",
				Assess: func(context.Context) (ActionState, string, error) {
					return 0, "", errors.New("deployments API 500")
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := ui.IsRaw()
			ui.SetDefault(ui.NewRawReporter())
			defer func() {
				if !prev {
					ui.SetDefault(ui.NewCardReporter())
				}
			}()

			cmd := &cobra.Command{Use: "t"}
			cmd.Flags().Bool("approve", true, "")
			cmd.Flags().Bool("dry-run", false, "")

			transitioned := false
			actions := []Action{
				tt.broken,
				{
					Op: ui.PlanModify, Action: "status", Type: "issue", Name: "EX-1",
					RequiresPriorSuccess: true,
					Assess: func(context.Context) (ActionState, string, error) {
						return ActionNeeded, "In Progress → Done", nil
					},
					Apply: func(context.Context) error {
						transitioned = true
						return nil
					},
				},
			}

			if err := runActions(cmd, context.Background(), actions); err == nil {
				t.Error("err = nil, want the failure to surface")
			}
			if transitioned {
				t.Error("the issue was moved behind a failure that never shipped")
			}
		})
	}
}

// TestRunActionsLaterAssessFailureDoesNotGate pins the "prior" in
// RequiresPriorSuccess. review and preview queue their notification
// after the status transition, and review's notify Assess ✗-rows a
// failed thread lookup — so a Slack outage must not withhold a
// transition for PRs that were created successfully.
func TestRunActionsLaterAssessFailureDoesNotGate(t *testing.T) {
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	defer func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	}()

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("approve", true, "")
	cmd.Flags().Bool("dry-run", false, "")

	transitioned := false
	actions := []Action{
		{
			Op: ui.PlanCreate, Action: "pr", Type: "repo", Name: "api",
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "branch → main", nil
			},
			Apply: func(context.Context) error { return nil },
		},
		{
			Op: ui.PlanModify, Action: "status", Type: "issue", Name: "EX-1",
			RequiresPriorSuccess: true,
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "In Progress → In Review", nil
			},
			Apply: func(context.Context) error {
				transitioned = true
				return nil
			},
		},
		{
			// Queued last, and it fails at assess — a ✗ row that is
			// not prior to the transition above it.
			Op: ui.PlanCreate, Action: "notify", Type: "channel", Name: "#reviews",
			Assess: func(context.Context) (ActionState, string, error) {
				return 0, "", errors.New("finding notification thread: host unavailable")
			},
		},
	}

	err := runActions(cmd, context.Background(), actions)
	if err == nil || !strings.Contains(err.Error(), "host unavailable") {
		t.Errorf("err = %v, want the notification failure to still surface", err)
	}
	if !transitioned {
		t.Error("a notification failure queued AFTER the transition withheld it")
	}
}

// TestRunActionsGatedActionRunsOnCleanPlan is the companion: the gate
// must not become a blanket refusal to transition.
func TestRunActionsGatedActionRunsOnCleanPlan(t *testing.T) {
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	defer func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	}()

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("approve", true, "")
	cmd.Flags().Bool("dry-run", false, "")

	transitioned := false
	actions := []Action{
		{
			Op: ui.PlanCreate, Action: "deploy", Type: "service", Name: "api",
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "ready", nil
			},
			Apply: func(context.Context) error { return nil },
		},
		{
			Op: ui.PlanModify, Action: "status", Type: "issue", Name: "EX-1",
			RequiresPriorSuccess: true,
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "In Progress → Done", nil
			},
			Apply: func(context.Context) error {
				transitioned = true
				return nil
			},
		},
	}

	if err := runActions(cmd, context.Background(), actions); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if !transitioned {
		t.Error("the gate withheld the transition from a run where everything succeeded")
	}
}

// TestRunActionsGroupScopedPriorFailureSeed pins the group-aware half
// of the PriorFailure seed: an assess failure in one group closes the
// gate for that group's later gated actions but not for a sibling
// group's — the seed mirrors the runner's group-scoped gate.
func TestRunActionsGroupScopedPriorFailureSeed(t *testing.T) {
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	defer func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	}()

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("approve", true, "")
	cmd.Flags().Bool("dry-run", false, "")

	removedA, removedB := false, false
	actions := []Action{
		{
			Op: ui.PlanDestroy, Action: "worktree", Type: "repo", Name: "api", Group: "ws-a",
			Assess: func(context.Context) (ActionState, string, error) {
				return 0, "", errors.New("stat failed")
			},
		},
		{
			Op: ui.PlanDestroy, Action: "directory", Type: "workspace", Name: "ws-a", Group: "ws-a",
			RequiresPriorSuccess: true,
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "", nil
			},
			Apply: func(context.Context) error {
				removedA = true
				return nil
			},
		},
		{
			Op: ui.PlanDestroy, Action: "directory", Type: "workspace", Name: "ws-b", Group: "ws-b",
			RequiresPriorSuccess: true,
			Assess: func(context.Context) (ActionState, string, error) {
				return ActionNeeded, "", nil
			},
			Apply: func(context.Context) error {
				removedB = true
				return nil
			},
		},
	}

	err := runActions(cmd, context.Background(), actions)
	if err == nil || !strings.Contains(err.Error(), "stat failed") {
		t.Errorf("err = %v, want the assess failure surfaced", err)
	}
	if removedA {
		t.Error("ws-a's directory was removed behind its own group's assess failure")
	}
	if !removedB {
		t.Error("ws-b's directory removal was withheld by a sibling group's failure")
	}
}

// TestRunActionsAllAssessFailedReturnsError covers the all-failed
// shape: nothing to apply, no PlanVerified misread, aggregate error.
func TestRunActionsAllAssessFailedReturnsError(t *testing.T) {
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	defer func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	}()

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().Bool("approve", true, "")
	cmd.Flags().Bool("dry-run", false, "")

	actions := []Action{
		{Op: ui.PlanCreate, Action: "a", Type: "t", Name: "one",
			Assess: func(context.Context) (ActionState, string, error) {
				return 0, "", errors.New("first")
			}},
		{Op: ui.PlanCreate, Action: "a", Type: "t", Name: "two",
			Assess: func(context.Context) (ActionState, string, error) {
				return 0, "", errors.New("second")
			}},
	}

	err := runActions(cmd, context.Background(), actions)
	if err == nil || !strings.Contains(err.Error(), "2 actions failed") || !strings.Contains(err.Error(), "first") {
		t.Errorf("err = %v, want aggregate mentioning count and first error", err)
	}
}
