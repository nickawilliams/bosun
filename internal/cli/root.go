package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCmd creates the root bosun command.
func NewRootCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "bosun",
		Short:         "Automate SDLC lifecycle tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().BoolP("version", "v", false, "print version information")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if v, _ := cmd.Flags().GetBool("version"); v {
			fmt.Println(version)
			return nil
		}
		return cmd.Help()
	}

	return cmd
}
