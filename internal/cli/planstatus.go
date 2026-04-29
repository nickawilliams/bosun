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
