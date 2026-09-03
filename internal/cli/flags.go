package cli

import "github.com/spf13/cobra"

// addProjectFlag registers --project/-p on a command, declaring that it
// uses project context. When set, it overrides CWD-based project
// detection (including which .bosun/config.yaml is loaded).
func addProjectFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("project", "p", "", "project path (overrides auto-detection)")
}

// addWorkspaceFlag registers --workspace/-w on a command, declaring
// that it uses workspace context.
func addWorkspaceFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("workspace", "w", "", "workspace name (e.g. feature/PROJ-123)")
}

// addIssueFlag registers --issue/-i on a command, declaring that it
// uses issue context.
func addIssueFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("issue", "i", "", "issue identifier (e.g. PROJ-123)")
}

// addAllFlag registers --all on a command, declaring that it can
// operate at explicit project scope — across every workspace in the
// project rather than one resolved workspace. Scope grammar (enforced
// by resolveWorkspaceScope): --all is mutually exclusive with the
// single-workspace targeting flags, and the workspace filter flags
// require project scope.
func addAllFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("all", false, "operate across all workspaces in the project")
}
