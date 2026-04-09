package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Deploy to preview environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			ui.Header("preview", issue)

			// TODO: Trigger deployment (phase 6)
			// TODO: Reply to notification thread (phase 5)

			transitionIssueStatus(cmd.Context(), issue, "review", "preview", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	return cmd
}
