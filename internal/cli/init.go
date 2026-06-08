package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new bosun project",
		Annotations: map[string]string{
			headerAnnotationTitle: "initialize project",
		},
		RunE: runInit,
	}

	addProjectFlag(cmd)
	cmd.Flags().Bool("no-detect", false, "skip auto-detection")
	cmd.Flags().Bool("quick", false, "only prompt for required values without defaults")
	cmd.Flags().String("workspace-root", "", "where workspaces are created")
	cmd.Flags().StringSlice("repositories", nil, "repository glob patterns (e.g. ./* or ~/Projects/myorg/*)")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	skipConfirm := isAutoApprove(cmd)
	quick, _ := cmd.Flags().GetBool("quick")
	noDetect, _ := cmd.Flags().GetBool("no-detect")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	bosunDir := filepath.Join(cwd, ".bosun")

	// Detect reinit — if .bosun/ already exists, confirm before proceeding.
	reinit := false
	if _, err := os.Stat(bosunDir); err == nil {
		reinit = true
		if !skipConfirm {
			confirmed, err := promptConfirm("Project already initialized — reconfigure?", false)
			if err != nil {
				return err
			}
			if !confirmed {
				ui.Skip("keeping existing configuration")
				return nil
			}
		}
	}

	// Check if we're inside an existing bosun project.
	if existing := config.FindProjectRoot(); existing != "" && existing != cwd {
		return fmt.Errorf(
			"already inside a bosun project at %s (nested projects are not supported)",
			existing,
		)
	}

	// On reinit, load existing values for use as defaults.
	var existingRepos []string
	var existingWSRoot string
	if reinit {
		existingRepos = viper.GetStringSlice("repositories")
		existingWSRoot = viper.GetString("workspace.root")
	}

	// Resolve repository globs.
	repositoryGlobs, _ := cmd.Flags().GetStringSlice("repositories")
	var detectedGlobs []string
	if len(repositoryGlobs) == 0 && !noDetect {
		if repositories := detectRepositories(cwd); len(repositories) > 0 {
			ui.CompleteWithDetail("detected repositories", repositories)
			detectedGlobs = defaultRepositoryGlobs(cwd, repositories)
		}
	}

	// Resolve workspace root.
	wsRoot, _ := cmd.Flags().GetString("workspace-root")

	// Prompt for project settings. In quick mode on reinit, skip if
	// values are already in config.
	needRepositories := len(repositoryGlobs) == 0
	needWS := wsRoot == ""
	if quick && reinit {
		if needRepositories && len(existingRepos) > 0 {
			repositoryGlobs = existingRepos
			needRepositories = false
		}
		if needWS && existingWSRoot != "" {
			wsRoot = existingWSRoot
			needWS = false
		}
	}
	if (needRepositories || needWS) && isInteractive() {
		// Determine defaults: prefer existing config on reinit, then
		// detected globs, then a sensible fallback.
		repoDefault := strings.Join(detectedGlobs, ", ")
		if reinit && len(existingRepos) > 0 {
			repoDefault = strings.Join(existingRepos, ", ")
		} else if repoDefault == "" {
			repoDefault = "., ./*"
		}
		wsDefault := ".workspaces"
		if reinit && existingWSRoot != "" {
			wsDefault = existingWSRoot
		}

		var repoField, wsField *defaultField
		var fields []huh.Field
		if needRepositories {
			var input *huh.Input
			input, repoField = newDefaultInput(repoDefault)
			fields = append(fields, input.
				Title(transformFieldTitle("Repository Patterns")).
				Description("Comma-separated globs, e.g. ./* or ~/Projects/myorg/*"))
		}
		if needWS {
			var input *huh.Input
			input, wsField = newDefaultInput(wsDefault)
			fields = append(fields, input.
				Title(transformFieldTitle("Workspace Root")).
				Description("Directory where workspaces are created"))
		}

		rewind := ui.NewCard(ui.CardInput, "project settings").PrintRewindable()
		if err := runForm(fields...); err != nil {
			return err
		}
		rewind()

		if repoField != nil {
			for _, g := range strings.Split(repoField.Resolved(), ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repositoryGlobs = append(repositoryGlobs, trimmed)
				}
			}
		}
		if wsField != nil {
			wsRoot = wsField.Resolved()
		}
	}

	// Apply defaults for anything still unresolved.
	if len(repositoryGlobs) == 0 && len(detectedGlobs) > 0 {
		repositoryGlobs = detectedGlobs
	}
	if len(repositoryGlobs) == 0 && isInteractive() {
		input, err := promptDefault(
			"No repositories detected. Enter repository patterns (comma-separated, or leave blank)",
			"")
		if err != nil {
			return err
		}
		if input != "" {
			for _, g := range strings.Split(input, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repositoryGlobs = append(repositoryGlobs, trimmed)
				}
			}
		}
	}
	if isDryRun(cmd) {
		ui.DryRun("would initialize bosun project")
		fields := ui.NewFields(
			"Config", ".bosun/config.yaml",
			"Repositories", strings.Join(repositoryGlobs, ", "),
		)
		if wsRoot != "" {
			fields = append(fields, ui.Field{Key: "Workspace Root", Value: wsRoot})
		}
		ui.Details("", fields)
		return nil
	}

	// Create .bosun/ directory.
	if err := os.MkdirAll(bosunDir, 0o755); err != nil {
		return fmt.Errorf("creating .bosun/: %w", err)
	}

	// Write config — fresh init creates the template; reinit does
	// targeted updates to preserve existing service configuration.
	configPath := filepath.Join(bosunDir, "config.yaml")
	if reinit {
		if len(repositoryGlobs) > 0 {
			if err := setConfigListValue(configPath, "repositories", repositoryGlobs); err != nil {
				return fmt.Errorf("updating repositories: %w", err)
			}
		}
		if wsRoot != "" {
			if err := setConfigValue(configPath, "workspace.root", wsRoot); err != nil {
				return fmt.Errorf("updating workspace.root: %w", err)
			}
		}
	} else {
		if err := writeInitConfig(configPath, wsRoot, repositoryGlobs); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
	}

	repositoryDisplay := strings.Join(repositoryGlobs, ", ")
	if repositoryDisplay == "" {
		repositoryDisplay = "(none — add repository patterns to .bosun/config.yaml)"
	}
	heading := "initialized bosun project"
	if reinit {
		heading = "updated project settings"
	}
	fields := ui.NewFields(
		"Config", configPath,
		"Repositories", repositoryDisplay,
	)
	if wsRoot != "" {
		fields = append(fields, ui.Field{Key: "Workspace Root", Value: wsRoot})
	}
	ui.Details(heading, fields)

	// Service configuration wizard — runs unless --yes.
	if isInteractive() {
		// Reload config so resolveGroup can read/write the new file.
		if err := config.Load(); err != nil {
			return err
		}

		for _, ig := range serviceInitGroups {
			group, ok := lookupGroup(ig.Group)
			if !ok {
				continue
			}

			if quick {
				// Quick mode: only resolve missing required keys.
				if err := resolveGroup(ig.Group, group); err != nil {
					return err
				}
				continue
			}

			// Full mode: confirm, then prompt for everything with defaults.
			confirmed, err := promptConfirm(fmt.Sprintf("Configure %s?", ig.Label), false)
			if err != nil {
				return err
			}
			if !confirmed {
				ui.Skip(ig.Label)
				continue
			}

			if err := resolveGroupReconfigure(ig.Group, group); err != nil {
				return err
			}

			// Custom setup for provider-specific flows (e.g., CI/CD workflow wizard).
			if ig.Setup != nil {
				if err := ig.Setup(); err != nil {
					return err
				}
			}
		}
	}

	// Style the command portion of each hint distinctly (bold +
	// primary) so it pops against the muted surrounding wording —
	// the user's eye lands on what to type before reading why.
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.Palette.Primary)
	hint := func(command, purpose string) string {
		return mutedStyle.Render("Run ") + cmdStyle.Render(command) + mutedStyle.Render(" "+purpose)
	}

	ui.NewCard(ui.CardInfo, "next steps").
		Text(hint("bosun doctor", "to verify connectivity")).
		Text(hint("bosun start --issue <issue>", "to begin work")).
		Print()

	return nil
}

// initGroup describes an optional service group for the init wizard.
type initGroup struct {
	Label    string       // Human-readable name for the confirmation prompt.
	Group    string       // Schema group name (e.g., "issue_tracker").
	Setup    func() error // Custom setup flow, replaces resolveGroup when set.
}

// serviceInitGroups defines the ordered list of optional service groups
// presented during interactive init.
var serviceInitGroups = []initGroup{
	{Label: "issue tracker", Group: "issue_tracker"},
	{Label: "code host", Group: "code_host"},
	{Label: "notifications", Group: "notification"},
	{Label: "CI/CD", Group: "cicd", Setup: setupGitHubActions},
}

// detectRepositories scans a directory for git repositories: the directory
// itself (if it contains .git/) and immediate children that do.
func detectRepositories(dir string) []string {
	var repositories []string

	// Check if the directory itself is a repository.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		repositories = append(repositories, filepath.Base(dir)+" (root)")
	}

	// Check children.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return repositories
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
			repositories = append(repositories, entry.Name())
		}
	}
	return repositories
}

// defaultRepositoryGlobs returns the default repository glob patterns based
// on what was detected. Uses "." for the root repository and "./*" for children.
func defaultRepositoryGlobs(dir string, detected []string) []string {
	var globs []string
	hasRoot := false
	hasChildren := false

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		hasRoot = true
	}
	for _, d := range detected {
		if !strings.HasSuffix(d, "(root)") {
			hasChildren = true
			break
		}
	}

	if hasRoot {
		globs = append(globs, ".")
	}
	if hasChildren {
		globs = append(globs, "./*")
	}
	return globs
}

// writeInitConfig writes the initial .bosun/config.yaml.
func writeInitConfig(path, wsRoot string, repositoryGlobs []string) error {
	var b strings.Builder

	b.WriteString("# Repository patterns (globs resolved to directories containing .git/)\n")
	b.WriteString("repositories:\n")
	if len(repositoryGlobs) > 0 {
		for _, g := range repositoryGlobs {
			fmt.Fprintf(&b, "  - %s\n", g)
		}
	} else {
		b.WriteString("  # - .          # this directory is a repository\n")
		b.WriteString("  # - ./*        # child directories that are repositories\n")
	}

	if wsRoot != "" {
		b.WriteString("\n# Where workspaces are created (relative to project root)\n")
		b.WriteString("workspace:\n")
		fmt.Fprintf(&b, "  root: %s\n", wsRoot)
	} else {
		b.WriteString("\n# Uncomment to enable worktree-based workspaces:\n")
		b.WriteString("# workspace:\n")
		b.WriteString("#   root: .workspaces\n")
	}

	b.WriteString("\n# Uncomment and configure as needed:\n")
	b.WriteString("# issue_tracker:\n")
	b.WriteString("#   provider: jira\n")
	b.WriteString("#   project: PROJ\n")
	b.WriteString("#\n")
	b.WriteString("# notification:\n")
	b.WriteString("#   provider: slack\n")
	b.WriteString("#   channel_review: code-review\n")
	b.WriteString("#   channel_release: releases\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
