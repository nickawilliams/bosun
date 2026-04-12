package cli

import (
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/ui"
)

// addStatusPlanItem adds a status transition item to the plan.
// If currentStatus is known (non-empty), it shows the full transition
// and detects no-ops. Otherwise shows just the target.
func addStatusPlanItem(plan *ui.Plan, issueKey, currentStatus, targetStatusKey string) {
	statusName, err := resolveStatus(targetStatusKey)
	if err != nil {
		return
	}

	if currentStatus != "" && strings.EqualFold(currentStatus, statusName) {
		plan.Add(ui.PlanNoChange, "Issue Status", issueKey, currentStatus)
	} else if currentStatus != "" {
		plan.Add(ui.PlanModify, "Update Issue Status", issueKey, fmt.Sprintf("%s → %s", currentStatus, statusName))
	} else {
		plan.Add(ui.PlanModify, "Update Issue Status", issueKey, fmt.Sprintf("→ %s", statusName))
	}
}
