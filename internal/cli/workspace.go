package cli

import (
	"context"
	"fmt"

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
		Annotations: map[string]string{
			headerAnnotationTitle: "create",
		},
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repoNames := args[1:]
			fromHead, _ := cmd.Flags().GetBool("from-head")
			rootCard(cmd, name).Print()

			if isDryRun(cmd) {
				ui.DryRun("Would create workspace")
				ui.Details("", ui.NewFields(
					"Repos", fmt.Sprintf("%v", repoNames),
					"From HEAD", fmt.Sprintf("%v", fromHead),
				))
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

			return ui.RunCard("Creating workspace", func() error {
				return mgr.Create(context.Background(), name, repos, fromHead)
			})
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [name] <repos...>",
		Short: "Add repos to an existing workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "add",
		},
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fromHead, _ := cmd.Flags().GetBool("from-head")

			// TODO(nick): distinguish name vs repo args when auto-detect is
			// implemented. For now, first arg is always the name.
			name := args[0]
			repoNames := args[1:]
			rootCard(cmd, name).Print()

			if isDryRun(cmd) {
				ui.DryRun("Would add repos: %v", repoNames)
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

			return ui.RunCard("Adding repos", func() error {
				return mgr.Add(context.Background(), name, repos, fromHead)
			})
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show workspace status",
		Annotations: map[string]string{
			headerAnnotationTitle: "status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(args)
			if err != nil {
				return err
			}
			rootCard(cmd, name).Print()

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			statuses, err := mgr.Status(context.Background(), name)
			if err != nil {
				return err
			}

			if len(statuses) == 0 {
				ui.Skip(fmt.Sprintf("No repos found in workspace %q", name))
				return nil
			}

			for _, s := range statuses {
				if s.Dirty {
					ui.Skip(fmt.Sprintf("%s: %s · dirty", s.Name, s.Branch))
				} else {
					ui.Complete(fmt.Sprintf("%s: %s · clean", s.Name, s.Branch))
				}
			}

			return nil
		},
	}
}

func newWorkspaceRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Remove a workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "remove",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(args)
			if err != nil {
				return err
			}
			rootCard(cmd, name).Print()
			force, _ := cmd.Flags().GetBool("force")

			if isDryRun(cmd) {
				ui.DryRun("Would remove workspace (force: %v)", force)
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
			return ui.RunCard("Removing workspace", func() error {
				return mgr.Remove(context.Background(), name, wsRepos, force)
			})
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")

	return cmd
}
