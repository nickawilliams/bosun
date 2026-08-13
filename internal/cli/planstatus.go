package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
)

// statusAction builds an Action for an issue status transition. Returns
// (action, true) on success, or (zero, false) if the tracker is nil or the
// target status key cannot be resolved — letting the caller skip gracefully.
func statusAction(tracker issue.Tracker, issueKey, currentStatus, targetStatusKey string) (Action, bool) {
	if tracker == nil {
		return Action{}, false
	}
	statusName, err := resolveStatus(targetStatusKey)
	if err != nil || statusName == "" {
		return Action{}, false
	}

	return Action{
		Op:     ui.PlanModify,
		Action: "status",
		Type:   "issue",
		Name:   issueKey,
		// The transition speaks for the whole run — "Done" claims the
		// deploy landed, "In Review" claims the PRs exist. Applying
		// it past a failure publishes a state that never happened to
		// a board the error message never reaches.
		//
		// Every command queues it behind the work it describes, which
		// is what the gate reads. Some then queue a notification
		// after it (review, preview); that one is deliberately not
		// prior, so a Slack outage cannot withhold a transition for
		// work that actually landed.
		RequiresPriorSuccess: true,
		Assess: func(_ context.Context) (ActionState, string, error) {
			if currentStatus != "" && strings.EqualFold(currentStatus, statusName) {
				return ActionCompleted, currentStatus, nil
			}
			if currentStatus != "" {
				return ActionNeeded, fmt.Sprintf("%s → %s", currentStatus, statusName), nil
			}
			return ActionNeeded, fmt.Sprintf("→ %s", statusName), nil
		},
		Apply: func(ctx context.Context) error {
			return tracker.SetStatus(ctx, issueKey, statusName)
		},
	}, true
}
