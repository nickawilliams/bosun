package cli

import (
	"context"
	"fmt"
	"os"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

// addWorkspaceFlag adds the shared --workspace flag to a command.
func addWorkspaceFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("workspace", "w", "", "workspace name (e.g. feature/PROJ-123)")
}

// resolveWorkspaceAddArgs splits positional args into a workspace name and a
// list of repositories. The name comes from --workspace, BOSUN_WORKSPACE, or
// CWD detection — falling back to args[0] only when none of those resolve.
func resolveWorkspaceAddArgs(cmd *cobra.Command, args []string) (string, []string, error) {
	if name, _ := cmd.Flags().GetString("workspace"); name != "" {
		return name, args, nil
	}
	if name := viper.GetString("workspace"); name != "" {
		return name, args, nil
	}
	if name, err := detectWorkspaceFromCWD(); err == nil && name != "" {
		return name, args, nil
	}
	if len(args) >= 1 {
		return args[0], args[1:], nil
	}
	return "", nil, fmt.Errorf("workspace name required: pass --workspace, run from inside a workspace, or include the name as the first argument")
}

// pickWorkspaceAddRepositories prompts the user to select repositories to add
// to the named workspace, excluding any already present. Returns the selected
// names, or nil if there is nothing left to add.
func pickWorkspaceAddRepositories(ctx context.Context, mgr *workspace.Manager, name string) ([]string, error) {
	all, err := resolveRepositories(nil)
	if err != nil {
		return nil, err
	}

	statuses, err := mgr.Status(ctx, name)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(statuses))
	for _, s := range statuses {
		existing[s.Name] = true
	}

	var available []string
	for _, r := range all {
		if !existing[r.Name] {
			available = append(available, r.Name)
		}
	}

	if len(available) == 0 {
		ui.Skip(fmt.Sprintf("all configured repositories are already in workspace %q", name))
		return nil, nil
	}

	if len(available) == 1 {
		return available, nil
	}

	if !isInteractive() {
		return nil, fmt.Errorf("no repositories specified (pass repository names or run interactively)")
	}

	opts := make([]huh.Option[string], len(available))
	for i, n := range available {
		opts[i] = huh.NewOption(n, n)
	}

	var selected []string
	repositorySlot := ui.NewSlot()
	repositorySlot.Show(ui.NewCard(ui.CardInput, "repositories").Tight())
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(opts...).
			Value(&selected),
	); err != nil {
		return nil, err
	}
	repositorySlot.Clear()

	if len(selected) > 0 {
		ui.SelectedMulti("repositories", selected)
	}
	return selected, nil
}

// argsToWorkspaceRepositories converts repository name arguments into
// workspace.Repository by resolving them against the configured repository globs.
func argsToWorkspaceRepositories(names []string) ([]workspace.Repository, error) {
	repositories, err := resolveRepositories(names)
	if err != nil {
		return nil, err
	}
	return cliRepositoriesToWorkspaceRepositories(repositories), nil
}

func newWorkspaceCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <repositories...>",
		Short: "Create a new workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "create",
		},
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			repositoryNames := args[1:]
			fromHead, _ := cmd.Flags().GetBool("from-head")
			rootCard(cmd, name).Print()

			repositories, err := argsToWorkspaceRepositories(repositoryNames)
			if err != nil {
				return err
			}

			plan := ui.NewPlan()
			for _, r := range repositories {
				plan.Add(ui.PlanCreate, "worktree", "repo", r.Name, name)
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			actions := []PlanAction{func() error {
				return mgr.Create(cmd.Context(), name, repositories, fromHead)
			}}

			return runPlanCard(cmd, plan, actions, PlanOpts{
				Confirm: false,
				Apply:   !isDryRun(cmd),
			})
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [name] [repositories...]",
		Short: "Add repositories to an existing workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "add",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fromHead, _ := cmd.Flags().GetBool("from-head")
			ctx := cmd.Context()

			name, repositoryNames, err := resolveWorkspaceAddArgs(cmd, args)
			if err != nil {
				return err
			}
			rootCard(cmd, name).Print()

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			if len(repositoryNames) == 0 {
				repositoryNames, err = pickWorkspaceAddRepositories(ctx, mgr, name)
				if err != nil {
					return err
				}
				if len(repositoryNames) == 0 {
					return nil
				}
			}

			repositories, err := argsToWorkspaceRepositories(repositoryNames)
			if err != nil {
				return err
			}

			plan := ui.NewPlan()
			for _, r := range repositories {
				plan.Add(ui.PlanCreate, "worktree", "repo", r.Name, name)
			}

			actions := []PlanAction{func() error {
				return mgr.Add(ctx, name, repositories, fromHead)
			}}

			return runPlanCard(cmd, plan, actions, PlanOpts{
				Confirm: false,
				Apply:   !isDryRun(cmd),
			})
		},
	}

	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")
	addWorkspaceFlag(cmd)

	return cmd
}

func newWorkspaceStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status [name]",
		Short: "Show workspace status",
		Annotations: map[string]string{
			headerAnnotationTitle: "status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(cmd, args)
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
				ui.Skip(fmt.Sprintf("no repositories found in workspace %q", name))
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

	addWorkspaceFlag(cmd)

	return cmd
}

func newWorkspaceRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [name]",
		Short: "Remove a workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "remove",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := resolveWorkspaceName(cmd, args)
			if err != nil {
				return err
			}
			rootCard(cmd, name).Print()
			force, _ := cmd.Flags().GetBool("force")
			yes, _ := cmd.Flags().GetBool("yes")

			// Pre-Plan confirmation (separate from the Plan Card gate).
			// Dry-run skips this — the user just wants to see what
			// would happen, not commit to removing.
			if !isDryRun(cmd) && !yes {
				if !isInteractive() {
					return fmt.Errorf("refusing to remove workspace %q non-interactively (pass --yes to confirm)", name)
				}
				confirmed, err := promptConfirm(fmt.Sprintf("Remove workspace %q?", name), false)
				if err != nil {
					return err
				}
				if !confirmed {
					ui.Skip("aborted")
					return nil
				}
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			repositories, err := resolveRepositories(nil)
			if err != nil {
				return err
			}

			plan := ui.NewPlan()
			for _, r := range repositories {
				plan.Add(ui.PlanDestroy, "worktree", "repo", r.Name, name)
			}

			// If we're standing inside the workspace we're about to delete,
			// move the process out so it doesn't operate from a directory
			// that's about to disappear, and we can guide the user back.
			var movedFrom string
			projectRoot := config.FindProjectRoot()
			if detected, _ := detectWorkspaceFromCWD(); detected == name && projectRoot != "" {
				cwd, _ := os.Getwd()
				if err := os.Chdir(projectRoot); err != nil {
					return fmt.Errorf("moving to project root: %w", err)
				}
				movedFrom = cwd
			}

			wsRepos := cliRepositoriesToWorkspaceRepositories(repositories)
			actions := []PlanAction{func() error {
				return mgr.Remove(cmd.Context(), name, wsRepos, force)
			}}

			if err := runPlanCard(cmd, plan, actions, PlanOpts{
				Confirm: false,
				Apply:   !isDryRun(cmd),
			}); err != nil {
				return err
			}

			if movedFrom != "" {
				ui.Info("shell is in a removed directory (%s); cd to %s", movedFrom, projectRoot)
			}
			return nil
		},
	}

	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	cmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	addWorkspaceFlag(cmd)

	return cmd
}
