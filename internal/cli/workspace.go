package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage git worktree workspaces",
	}

	cmd.AddCommand(
		newWorkspaceCreateCmd(),
		newWorkspaceAddCmd(),
		newWorkspaceStatusCmd(),
		newWorkspaceRmCmd(),
	)

	return cmd
}

func newWorkspaceCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <repos...>",
		Short: "Create a new workspace",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromHead, _ := cmd.Flags().GetBool("from-head")
			fmt.Printf("[stub] Would create workspace %q for repos %v (from-head: %v)\n",
				args[0], args[1:], fromHead)
			return nil
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name] <repos...>",
		Short: "Add repos to an existing workspace",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("[stub] Would add repos to workspace: %v\n", args)
			return nil
		},
	}
}

func newWorkspaceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show workspace status",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "(auto-detect)"
			if len(args) > 0 {
				name = args[0]
			}
			fmt.Printf("[stub] Would show status for workspace %q\n", name)
			return nil
		},
	}
}

func newWorkspaceRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Remove a workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "(auto-detect)"
			if len(args) > 0 {
				name = args[0]
			}
			force, _ := cmd.Flags().GetBool("force")
			fmt.Printf("[stub] Would remove workspace %q (force: %v)\n", name, force)
			return nil
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
