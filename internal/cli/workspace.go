package cli

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/workspace"
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

// argsToWorkspaceRepos converts repo name arguments into workspace.Repo
// by resolving them against the configured repo globs.
func argsToWorkspaceRepos(names []string) ([]workspace.Repo, error) {
	repos, err := resolveRepos(names)
	if err != nil {
		return nil, err
	}
	return cliReposToWorkspaceRepos(repos), nil
}

func newWorkspaceCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <repos...>",
		Short: "Create a new workspace",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repoNames := args[1:]
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would create workspace %q for repos %v (from-head: %v)\n",
					name, repoNames, fromHead)
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			repos, err := argsToWorkspaceRepos(repoNames)
			if err != nil {
				return err
			}

			if err := mgr.Create(context.Background(), name, repos, fromHead); err != nil {
				return err
			}

			fmt.Printf("Created workspace %q\n", name)
			return nil
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [name] <repos...>",
		Short: "Add repos to an existing workspace",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromHead, _ := cmd.Flags().GetBool("from-head")

			// TODO(nick): distinguish name vs repo args when auto-detect is
			// implemented. For now, first arg is always the name.
			name := args[0]
			repoNames := args[1:]

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would add repos %v to workspace %q\n", repoNames, name)
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			repos, err := argsToWorkspaceRepos(repoNames)
			if err != nil {
				return err
			}

			if err := mgr.Add(context.Background(), name, repos, fromHead); err != nil {
				return err
			}

			fmt.Printf("Added repos to workspace %q\n", name)
			return nil
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show workspace status",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(args)
			if err != nil {
				return err
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			statuses, err := mgr.Status(context.Background(), name)
			if err != nil {
				return err
			}

			if len(statuses) == 0 {
				fmt.Printf("No repos found in workspace %q\n", name)
				return nil
			}

			fmt.Printf("Workspace: %s\n\n", name)
			for _, s := range statuses {
				state := "clean"
				if s.Dirty {
					state = "dirty"
				}
				fmt.Printf("  %-20s %-40s %s\n", s.Name, s.Branch, state)
			}

			return nil
		},
	}
}

func newWorkspaceRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Remove a workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(args)
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would remove workspace %q (force: %v)\n", name, force)
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			repos, err := resolveRepos(nil)
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			if err := mgr.Remove(context.Background(), name, wsRepos, force); err != nil {
				return err
			}

			fmt.Printf("Removed workspace %q\n", name)
			return nil
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
