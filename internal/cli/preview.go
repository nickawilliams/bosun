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
			ui.Muted("[stub] Would deploy %s to preview", issue)
			ui.Muted("  - Trigger ephemeral environment deployment")
			ui.Muted("  - Reply to review notification with preview URL")
			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}
