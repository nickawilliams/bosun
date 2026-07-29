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
	cmd.Flags().Bool("yes", true, "")
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
	cmd.Flags().Bool("yes", true, "")
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
