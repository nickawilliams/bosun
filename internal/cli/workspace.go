package cli

import (
	"context"

	"github.com/nickawilliams/bosun/internal/ui"
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
				ui.DryRun("Would create workspace %q for repos %v (from-head: %v)",
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

			err = ui.WithSpinner("Creating workspace...", func() error {
				return mgr.Create(context.Background(), name, repos, fromHead)
			})
			if err != nil {
				return err
			}

			ui.Success("Created workspace %q", name)
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
				ui.DryRun("Would add repos %v to workspace %q", repoNames, name)
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

			err = ui.WithSpinner("Adding repos...", func() error {
				return mgr.Add(context.Background(), name, repos, fromHead)
			})
			if err != nil {
				return err
			}

			ui.Success("Added repos to workspace %q", name)
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
				ui.Warning("No repos found in workspace %q", name)
				return nil
			}

			ui.Bold("Workspace: %s", name)

			table := ui.NewTable(
				ui.Column{Header: "Repo"},
				ui.Column{Header: "Branch"},
				ui.Column{Header: "Status"},
			)
			for _, s := range statuses {
				state := "clean"
				if s.Dirty {
					state = "dirty"
				}
				table.AddRow(s.Name, s.Branch, state)
			}
			table.Render()

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
				ui.DryRun("Would remove workspace %q (force: %v)", name, force)
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
			err = ui.WithSpinner("Removing workspace...", func() error {
				return mgr.Remove(context.Background(), name, wsRepos, force)
			})
			if err != nil {
				return err
			}

			ui.Success("Removed workspace %q", name)
			return nil
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
