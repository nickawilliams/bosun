package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
)

// transitionIssueStatus handles the common pattern of validating the current
// status and transitioning to a new one. Graceful — logs warnings instead of
// failing when tracker is not configured. This function always executes;
// callers gate with confirmPlan before calling.
func transitionIssueStatus(ctx context.Context, issueKey, expectedStatusKey, targetStatusKey string) {
	tracker, trackerErr := newIssueTracker()
	if trackerErr != nil {
		ui.NewCard(ui.CardSkipped, fmt.Sprintf("Issue tracker: %v", trackerErr)).Print()
		return
	}

	statusName, err := resolveStatus(targetStatusKey)
	if err != nil {
		ui.NewCard(ui.CardSkipped, fmt.Sprintf("Status mapping: %v", err)).Print()
		return
	}

	if err := validateStageTransition(ctx, tracker, issueKey, expectedStatusKey); err != nil {
		ui.NewCard(ui.CardFailed, fmt.Sprintf("Stage validation: %v", err)).Print()
		return
	}

	err = ui.RunCard(fmt.Sprintf("Setting status to %s", statusName), func() error {
		return tracker.SetStatus(ctx, issueKey, statusName)
	})
	if err != nil {
		ui.NewCard(ui.CardFailed, fmt.Sprintf("Set status: %v", err)).Print()
	}
}
