package cli

import (
	"context"

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
	Op     ui.PlanOp // Operation type shown in the plan (create/modify/destroy).
	Label  string    // Action label (e.g. "Pull Request", "Branch").
	Target string    // Target name (e.g. repository name, issue key).

	// Assess queries current state and returns the action's readiness plus a
	// detail string for the plan item (e.g. "#42" for an existing PR,
	// "branch → main" for a new one).
	Assess func(ctx context.Context) (ActionState, string, error)

	// Apply performs the action. Only called when Assess returned ActionNeeded.
	Apply func(ctx context.Context) error
}

// runActions assesses each action, builds a plan, and executes only the
// actions that are still needed. It delegates to runPlanCard for confirmation
// and apply.
func runActions(cmd *cobra.Command, ctx context.Context, actions []Action) error {
	plan := ui.NewPlan()
	var pending []Action

	for _, a := range actions {
		state, detail, err := a.Assess(ctx)
		if err != nil {
			return err
		}
		switch state {
		case ActionNeeded:
			plan.Add(a.Op, a.Label, a.Target, detail)
			pending = append(pending, a)
		case ActionCompleted:
			plan.Add(ui.PlanNoChange, a.Label, a.Target, detail)
		case ActionSkipped:
			// Omit from plan entirely.
		}
	}

	applyFns := make([]PlanAction, len(pending))
	for i, a := range pending {
		applyFns[i] = func() error { return a.Apply(ctx) }
	}

	return runPlanCard(cmd, plan, applyFns)
}
