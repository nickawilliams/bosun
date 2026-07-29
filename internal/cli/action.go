package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ActionState describes whether an action needs to be performed.
type ActionState int

const (
	// ActionNeeded means the action has not been performed yet.
	ActionNeeded ActionState = iota
	// ActionCompleted means the action has already been performed.
	ActionCompleted
	// ActionSkipped means the action is not applicable and should be
	// omitted from the plan entirely.
	ActionSkipped
)

// Action pairs a plan item with assess/apply logic. Assess queries external
// state to determine whether the action is needed, and Apply performs it.
type Action struct {
	Op     ui.PlanOp  // Operation type shown in the plan (create/modify/destroy).
	OpRef  *ui.PlanOp // If set, overrides Op after Assess (allows Assess to change the op).
	Action string     // Operation noun: "deploy", "branch", "notify", "status".
	Type   string     // Subject category: "repo", "env", "channel", "issue".
	Name   string     // Subject identifier: "api", "brave-falcon", "#reviews".

	// Assess queries current state and returns the action's readiness plus a
	// detail string for the plan item (e.g. "#42" for an existing PR,
	// "branch → main" for a new one).
	Assess func(ctx context.Context) (ActionState, string, error)

	// Apply performs the action. Only called when Assess returned ActionNeeded.
	Apply func(ctx context.Context) error

	// DetailRef, when set, lets Apply supersede the assessed detail on
	// the plan row after it runs — the assessed detail carries a
	// "known after apply" placeholder, Apply fills the ref with the
	// resolved value (created issue key, returned URL), and the plan
	// card's final frame renders it. Sibling of OpRef.
	DetailRef *ui.DetailRef
}

// op returns the effective operation, preferring OpRef if set.
func (a *Action) op() ui.PlanOp {
	if a.OpRef != nil {
		return *a.OpRef
	}
	return a.Op
}

// runActions assesses each action, builds a plan, and executes only the
// actions that are still needed. It delegates to runPlanCard for
// confirmation and apply.
//
// A failed assessment is a ✗ plan row, not a run-wide abort: the
// commands route per-target failures through Assess errors (release's
// st.err rows, prerelease's rt.tagErr, review's per-repo prErr) with
// the documented contract that siblings proceed. The healthy actions
// plan and apply normally; the first assess error is returned after
// the run so the exit code still reflects the partial failure.
func runActions(cmd *cobra.Command, ctx context.Context, actions []Action) error {
	plan := ui.NewPlan()
	var pending []Action
	var assessErr error
	assessFailures := 0

	// Show a spinner while assessing actions (may involve API calls).
	rewind, spinErr := ui.RunCardRewindable("assessing", func() error {
		for i := range actions {
			a := &actions[i]
			state, detail, err := a.Assess(ctx)
			if err != nil {
				plan.Add(ui.PlanFailed, a.Action, a.Type, a.Name, err.Error())
				if assessErr == nil {
					assessErr = err
				}
				assessFailures++
				continue
			}
			switch state {
			case ActionNeeded:
				if a.DetailRef != nil {
					plan.AddWithDetailRef(a.op(), a.Action, a.Type, a.Name, detail, a.DetailRef)
				} else {
					plan.Add(a.op(), a.Action, a.Type, a.Name, detail)
				}
				if a.op() != ui.PlanDetail {
					pending = append(pending, *a)
				}
			case ActionCompleted:
				plan.Add(ui.PlanNoChange, a.Action, a.Type, a.Name, detail)
			case ActionSkipped:
				// Omit from plan entirely.
			}
		}
		return nil
	})
	if spinErr != nil {
		return spinErr
	}

	// Rewind the spinner to replace it with the plan card.
	if rewind != nil {
		rewind()
	}

	applyFns := make([]PlanAction, len(pending))
	for i, a := range pending {
		applyFns[i] = func() error { return a.Apply(ctx) }
	}

	if err := runPlanCard(cmd, plan, applyFns, DefaultPlanOpts(cmd)); err != nil {
		return err
	}
	if assessErr != nil {
		if assessFailures > 1 {
			return fmt.Errorf("%d actions failed to assess; first: %w", assessFailures, assessErr)
		}
		return assessErr
	}
	return nil
}
