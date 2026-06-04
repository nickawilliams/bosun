package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		newWorkspaceDeleteCmd(),
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

// pickWorkspaceRmRepositories prompts the user to select repositories to
// remove from the named workspace. Enumerates from the workspace's
// current repo statuses (so the picker shows only repos actually
// present). Returns the selected names, or nil if there is nothing to
// remove.
func pickWorkspaceRmRepositories(ctx context.Context, mgr *workspace.Manager, name string) ([]string, error) {
	statuses, err := mgr.Status(ctx, name)
	if err != nil {
		return nil, err
	}

	if len(statuses) == 0 {
		ui.Skip(fmt.Sprintf("workspace %q has no repositories to remove", name))
		return nil, nil
	}

	if len(statuses) == 1 {
		return []string{statuses[0].Name}, nil
	}

	if !isInteractive() {
		return nil, fmt.Errorf("no repositories specified (pass repository names or run interactively)")
	}

	opts := make([]huh.Option[string], len(statuses))
	for i, s := range statuses {
		opts[i] = huh.NewOption(s.Name, s.Name)
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
		Use:   "rm [repositories...]",
		Short: "Remove repositories from an existing workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "rm",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			name := cc.Workspace
			repositoryNames := args
			force, _ := cmd.Flags().GetBool("force")
			ctx := cmd.Context()

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			if len(repositoryNames) == 0 {
				repositoryNames, err = pickWorkspaceRmRepositories(ctx, mgr, name)
				if err != nil {
					return err
				}
				if len(repositoryNames) == 0 {
					return nil
				}
			}

			repositories, err := resolveRepositories(repositoryNames)
			if err != nil {
				return err
			}
			wsRepos := cliRepositoriesToWorkspaceRepositories(repositories)

			plan := ui.NewPlan()
			for _, r := range repositories {
				plan.Add(ui.PlanDestroy, "worktree", "repo", r.Name, name)
			}

			// movedFrom is set inside the apply action if the process is
			// standing in a worktree about to disappear; surfaced as a
			// "cd back" hint after the plan finalizes.
			var movedFrom string
			projectRoot := config.FindProjectRoot()
			actions := []PlanAction{func() error {
				if cwd, err := os.Getwd(); err == nil && projectRoot != "" {
					wsRoot := viper.GetString("workspace.root")
					if !filepath.IsAbs(wsRoot) {
						wsRoot = filepath.Join(projectRoot, wsRoot)
					}
					for _, r := range repositoryNames {
						wtPath := filepath.Join(wsRoot, name, r)
						if cwd == wtPath || strings.HasPrefix(cwd+string(os.PathSeparator), wtPath+string(os.PathSeparator)) {
							if err := os.Chdir(projectRoot); err != nil {
								return fmt.Errorf("moving to project root: %w", err)
							}
							movedFrom = cwd
							break
						}
					}
				}
				return mgr.RemoveRepositories(ctx, name, wsRepos, repositoryNames, force)
			}}

			if err := runPlanCard(cmd, plan, actions, PlanOpts{
				Confirm: true,
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

func newWorkspaceDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete [name]",
		Short: "Delete a workspace",
		Annotations: map[string]string{
			headerAnnotationTitle: "delete",
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

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			repositories, err := resolveRepositories(nil)
			if err != nil {
				return err
			}
			wsRepos := cliRepositoriesToWorkspaceRepositories(repositories)

			plan := ui.NewPlan()
			for _, r := range repositories {
				plan.Add(ui.PlanDestroy, "worktree", "repo", r.Name, name)
			}

			// movedFrom is set inside the apply action if the process is
			// standing in the workspace about to disappear; surfaced as a
			// "cd back" hint after the plan finalizes.
			var movedFrom string
			projectRoot := config.FindProjectRoot()
			actions := []PlanAction{func() error {
				if detected, _ := detectWorkspaceFromCWD(); detected == name && projectRoot != "" {
					cwd, _ := os.Getwd()
					if err := os.Chdir(projectRoot); err != nil {
						return fmt.Errorf("moving to project root: %w", err)
					}
					movedFrom = cwd
				}
				return mgr.Remove(cmd.Context(), name, wsRepos, force)
			}}

			if err := runPlanCard(cmd, plan, actions, PlanOpts{
				Confirm: true,
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
