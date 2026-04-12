package cli

import (
	"github.com/spf13/cobra"
)

func newPrereleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prerelease",
		Short: "Prepare release artifacts",
		Annotations: map[string]string{
			headerAnnotationTitle: "pre-release",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			// TODO: Create releases (phase 4)
			// TODO: Notify release channel (phase 5)

			transitionIssueStatus(cmd.Context(), issue, "preview", "ready_for_release", isDryRun(cmd))
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	return cmd
}
