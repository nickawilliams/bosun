package cli

import (
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

			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			transitionIssueStatus(cmd.Context(), issue, "review", "preview", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
