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
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage git worktree workspaces",
	}

	cmd.AddCommand(
		newWorkspaceCreateCmd(),
		newWorkspaceAddCmd(),
		newWorkspaceRmCmd(),
	)

	return cmd
}

// pickOrPromptWorkspace presents an interactive picker of existing
// workspaces. Returns the selected workspace name, or empty string in
// non-interactive mode, when no workspaces exist, or when the user
// cancels. Falls back to a free-text prompt when the workspace root is
// configured but empty so the caller still has a path to supply a name.
func pickOrPromptWorkspace() string {
	if !isInteractive() {
		return ""
	}

	mgr, err := newWorkspaceManager()
	if err != nil {
		return promptRequired("Workspace")
	}

	names, err := mgr.List()
	if err != nil || len(names) == 0 {
		return promptRequired("Workspace")
	}

	if len(names) == 1 {
		return names[0]
	}

	opts := make([]huh.Option[string], len(names))
	for i, n := range names {
		opts[i] = huh.NewOption(n, n)
	}

	var selected string
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, "select workspace").Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Value(&selected),
	); err != nil {
		return ""
	}
	slot.Clear()
	return selected
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

	addProjectFlag(cmd)
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}

func newWorkspaceAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [repositories...]",
		Short: "Add repositories to an existing workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "add",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			name := cc.Workspace
			repositoryNames := args
			fromHead, _ := cmd.Flags().GetBool("from-head")
			ctx := cmd.Context()

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

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

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
			cc := commandContext(cmd)
			// Positional name wins over the resolution chain — it's an
			// explicit on-the-line target. Otherwise fall through to the
			// standard --workspace/env/CWD/prompt path.
			if len(args) > 0 {
				cc.Workspace = args[0]
			}
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			name := cc.Workspace
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

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	cmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")

	return cmd
}
