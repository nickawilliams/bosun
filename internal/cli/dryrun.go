package cli

import "github.com/spf13/cobra"

// isDryRun returns true if the --dry-run flag is set on the root command.
func isDryRun(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("dry-run")
	return v
}
