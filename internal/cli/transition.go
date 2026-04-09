package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
)

// transitionIssueStatus handles the common pattern of validating the current
// status and transitioning to a new one. Graceful — logs warnings instead of
// failing when tracker is not configured.
func transitionIssueStatus(ctx context.Context, issueKey, expectedStatusKey, targetStatusKey string, dryRun bool) {
	if dryRun {
		if statusName, err := resolveStatus(targetStatusKey); err == nil {
			ui.DryRun("Would set status to %s", statusName)
		}
		return
	}

	tracker, trackerErr := newIssueTracker()
	if trackerErr != nil {
		ui.Skip(fmt.Sprintf("Issue tracker: %v", trackerErr))
		return
	}

	statusName, err := resolveStatus(targetStatusKey)
	if err != nil {
		ui.Skip(fmt.Sprintf("Status mapping: %v", err))
		return
	}

	if err := validateStageTransition(ctx, tracker, issueKey, expectedStatusKey); err != nil {
		ui.Fail(fmt.Sprintf("Stage validation: %v", err))
		return
	}

	err = ui.WithSpinner(fmt.Sprintf("Setting status to %s...", statusName), func() error {
		return tracker.SetStatus(ctx, issueKey, statusName)
	})
	if err != nil {
		ui.Fail(fmt.Sprintf("Set status: %v", err))
	} else {
		ui.Complete(fmt.Sprintf("Set status to %s", statusName))
	}
}
