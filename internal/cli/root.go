package cli

import (
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const groupLifecycle = "lifecycle"

// NewRootCmd creates the root bosun command.
func NewRootCmd() *cobra.Command {
	cobra.EnableCommandSorting = false

	cmd := &cobra.Command{
		Use:           "bosun",
		Short:         "Automate SDLC lifecycle tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := config.Load(); err != nil {
				return err
			}
			ui.ApplyColorMode(viper.GetString("color_mode"))

			// Determine output mode: raw when stdout isn't a TTY, or
			// when the command explicitly declares raw output (annotation
			// or --output flag).
			raw := !ui.IsTerminal() ||
				cmd.Annotations["output"] == "raw" ||
				(cmd.Flag("output") != nil && cmd.Flag("output").Value.String() != "")

			if raw {
				ui.SetDefault(ui.NewRawReporter())
			} else {
				ui.ApplyDisplayMode(viper.GetString("display_mode"))
				ui.BeginTimeline()
			}
			return nil
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if !ui.IsRaw() {
				ui.EndTimeline()
			}
		},
	}

	cmd.PersistentFlags().Bool("dry-run", false, "show what would happen without making changes")
	cmd.PersistentFlags().BoolP("yes", "y", false, "skip confirmation prompt")
	cmd.PersistentFlags().Bool("interactive", false, "prompt for configurable values")

	cmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle"},
	)

	// Lifecycle commands — ordered by lifecycle stage.
	lifecycle := []*cobra.Command{
		newCreateCmd(),
		newStartCmd(),
		newReviewCmd(),
		newPreviewCmd(),
		newPrereleaseCmd(),
		newReleaseCmd(),
		newCleanupCmd(),
	}
	for _, sub := range lifecycle {
		sub.GroupID = groupLifecycle
	}
	cmd.AddCommand(lifecycle...)

	// Utility commands — ungrouped so they merge with fang's
	// help/completion in the default "Commands" section.
	cmd.AddCommand(
		newConfigCmd(),
		newDoctorCmd(),
		newInitCmd(),
		newStatusCmd(),
		newWorkspaceCmd(),
	)

	// Hidden commands.
	cmd.AddCommand(newDemoCmd())
	cmd.AddCommand(newCaptainCmd())

	return cmd
}
