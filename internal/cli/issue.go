package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// addIssueFlag adds the shared --issue flag to a command.
func addIssueFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("issue", "i", "", "issue identifier (e.g. PROJ-123)")
}

// resolveIssue returns the issue identifier from the resolution chain:
// (1) --issue flag, (2) BOSUN_ISSUE env var. Workspace path and branch
// name derivation will be added in a later phase.
func resolveIssue(cmd *cobra.Command) (string, error) {
	// Check the flag first.
	if issue, _ := cmd.Flags().GetString("issue"); issue != "" {
		return issue, nil
	}

	// Check Viper (env var BOSUN_ISSUE via AutomaticEnv).
	if issue := viper.GetString("issue"); issue != "" {
		return issue, nil
	}

	// TODO(nick): workspace path derivation (phase 2)
	// TODO(nick): git branch name derivation (phase 2)

	return "", fmt.Errorf(
		"issue not specified: use --issue, set BOSUN_ISSUE, or run from a workspace",
	)
}
