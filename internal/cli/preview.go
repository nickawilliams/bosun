package cli

import (
	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		Annotations: map[string]string{
			headerAnnotationTitle: "deploy",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			ctx := cmd.Context()
			tracker, _ := newIssueTracker()

			// --- Plan + Apply ---
			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			var actions []Action
			if sa, ok := statusAction(tracker, issue, "", "preview"); ok {
				actions = append(actions, sa)
			}

			return runActions(cmd, ctx, actions)
		},
	}

	addIssueFlag(cmd)
	return cmd
}
