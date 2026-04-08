package cli

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
)

// NewRootCmd creates the root bosun command.
func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "bosun",
		Short:         "Automate SDLC lifecycle tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return config.Load()
		},
	}

	cmd.PersistentFlags().Bool("dry-run", false, "show what would happen without making changes")
	cmd.Flags().BoolP("version", "v", false, "print version information")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Println(version)
			return nil
		}
		return cmd.Help()
	}

	cmd.AddCommand(
		newCreateCmd(),
		newStartCmd(),
		newReviewCmd(),
		newPreviewCmd(),
		newPrereleaseCmd(),
		newReleaseCmd(),
		newCleanupCmd(),
		newStatusCmd(),
		newWorkspaceCmd(),
	)

	return cmd
}
