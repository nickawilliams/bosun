package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		Annotations: map[string]string{
			headerAnnotationTitle: "preview deploy",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			// --- Resolve ---
			statusName, statusErr := resolveStatus("preview")

			// --- Plan ---
			plan := ui.NewPlan()
			if statusErr == nil {
				plan.Add(ui.PlanModify, "Update Issue Status", issue, fmt.Sprintf("→ %s", statusName))
			}

			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			if !confirmPlan(cmd, plan) {
				return nil
			}

			// --- Apply ---
			transitionIssueStatus(cmd.Context(), issue, "review", "preview")
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
