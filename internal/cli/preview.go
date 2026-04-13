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

			ctx := cmd.Context()
			statusName, _ := resolveStatus("preview")

			// --- Plan + Apply ---
			plan := ui.NewPlan()
			addStatusPlanItem(plan, issue, "", "preview")
			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			tracker, trackerErr := newIssueTracker()
			var actions []PlanAction
			if trackerErr == nil && statusName != "" {
				actions = append(actions, func() error {
					return tracker.SetStatus(ctx, issue, statusName)
				})
			}

			if err := runPlanCard(cmd, plan, actions); err != nil {
				return err
			}
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
