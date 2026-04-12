package cli

import (
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

			// --- Plan ---
			plan := ui.NewPlan()
			addStatusPlanItem(plan, issue, "", "preview")

			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			if err := confirmPlan(cmd, plan); err != nil {
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
