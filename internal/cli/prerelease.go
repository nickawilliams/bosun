package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newPrereleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prerelease",
		Short: "Prepare release artifacts",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			bump, _ := cmd.Flags().GetString("bump")

			if isDryRun(cmd) {
				ui.DryRun("Would prepare release for %s (bump: %s)", issue, bump)
				ui.Muted("  - Create release/tag per repo")
				ui.Muted("  - Notify release channel")
				if statusName, err := resolveStatus("ready_for_release"); err == nil {
					ui.Item("Status:", fmt.Sprintf("→ %s", statusName))
				}
				return nil
			}

			// TODO: Create releases (phase 4)
			// TODO: Notify release channel (phase 5)

			ctx := cmd.Context()
			tracker, trackerErr := newIssueTracker()
			if trackerErr != nil {
				ui.Warning("Issue tracker not configured: %v", trackerErr)
			} else {
				statusName, err := resolveStatus("ready_for_release")
				if err != nil {
					ui.Warning("Status mapping: %v", err)
				} else {
					if err := validateStageTransition(ctx, tracker, issue, "preview"); err != nil {
						return err
					}
					err = ui.WithSpinner(fmt.Sprintf("Setting status to %s...", statusName), func() error {
						return tracker.SetStatus(ctx, issue, statusName)
					})
					if err != nil {
						ui.Warning("Failed to set status: %v", err)
					} else {
						ui.Success("Set %s to %s", issue, statusName)
					}
				}
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")

	return cmd
}
