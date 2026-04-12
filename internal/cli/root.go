package cli

import (
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	groupLifecycle = "lifecycle"
	groupCommands  = "commands"
)

// NewRootCmd creates the root bosun command.
func NewRootCmd(version string) *cobra.Command {
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
			return nil
		},
	}

	cmd.PersistentFlags().Bool("dry-run", false, "show what would happen without making changes")
	cmd.PersistentFlags().BoolP("yes", "y", false, "skip confirmation prompt")
	cmd.Flags().BoolP("version", "v", false, "print version information")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("version"); v {
			ui.Bold("%s", version)
			return nil
		}
		return cmd.Help()
	}

	setStyledHelp(cmd)

	cmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle"},
		&cobra.Group{ID: groupCommands, Title: "Commands"},
	)

	// Lifecycle commands — ordered by lifecycle stage.
	lifecycle := []*cobra.Command{
		newInitCmd(),
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

	// Utility commands — alphabetical.
	utility := []*cobra.Command{
		newConfigCmd(),
		newDoctorCmd(),
		newStatusCmd(),
		newWorkspaceCmd(),
	}
	for _, sub := range utility {
		sub.GroupID = groupCommands
	}
	cmd.AddCommand(utility...)

	// Hidden commands.
	cmd.AddCommand(newDemoCmd())

	return cmd
}
