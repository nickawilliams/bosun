package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
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

	cmd.Flags().Bool("no-detect", false, "skip auto-detection")
	cmd.Flags().Bool("quick", false, "only prompt for required values without defaults")
	cmd.Flags().String("workspace-root", "", "where workspaces are created")
	cmd.Flags().StringSlice("repositories", nil, "repository glob patterns (e.g. ./* or ~/Projects/myorg/*)")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	rootCard(cmd).Print()
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
				ui.Skip("Keeping existing configuration")
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
		existingWSRoot = viper.GetString("workspace_root")
	}

	// Resolve repository globs.
	repositoryGlobs, _ := cmd.Flags().GetStringSlice("repositories")
	var detectedGlobs []string
	if len(repositoryGlobs) == 0 && !noDetect {
		if repositories := detectRepositories(cwd); len(repositories) > 0 {
			ui.CompleteWithDetail("Detected repositories", repositories)
			detectedGlobs = defaultRepositoryGlobs(cwd, repositories)
		}
	}

	// Resolve workspace_root.
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
				Title("Repository patterns").
				Description("Comma-separated globs, e.g. ./* or ~/Projects/myorg/*"))
		}
		if needWS {
			var input *huh.Input
			input, wsField = newDefaultInput(wsDefault)
			fields = append(fields, input.
				Title("Workspace root").
				Description("Directory where workspaces are created"))
		}

		rewind := ui.NewCard(ui.CardInput, "Project Settings").PrintRewindable()
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
		ui.DryRun("Would initialize bosun project")
		fields := ui.NewFields(
			"Config", ".bosun/config.yaml",
			"Repositories", strings.Join(repositoryGlobs, ", "),
		)
		if wsRoot != "" {
			fields = append(fields, ui.Field{Key: "Workspace root", Value: wsRoot})
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
			if err := setConfigValue(configPath, "workspace_root", wsRoot); err != nil {
				return fmt.Errorf("updating workspace_root: %w", err)
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
	heading := "Initialized bosun project"
	if reinit {
		heading = "Updated project settings"
	}
	fields := ui.NewFields(
		"Config", configPath,
		"Repositories", repositoryDisplay,
	)
	if wsRoot != "" {
		fields = append(fields, ui.Field{Key: "Workspace root", Value: wsRoot})
	}
	ui.Details(heading, fields)

	// Service configuration wizard — runs unless --yes.
	if isInteractive() {
		// Reload config so resolveGroup can read/write the new file.
		if err := config.Load(); err != nil {
			return err
		}

		for _, ig := range serviceInitGroups {
			if quick {
				// Quick mode: only resolve missing required keys.
				providerGroup, ok := lookupGroup(ig.Provider)
				if !ok {
					continue
				}
				if err := resolveGroup(ig.Provider, providerGroup); err != nil {
					return err
				}
				if ig.Setup != nil {
					// Custom setups handle their own quick logic.
					continue
				}
				if ig.Detail != "" {
					detailGroup, ok := lookupGroup(ig.Detail)
					if !ok {
						continue
					}
					if err := resolveGroup(ig.Detail, detailGroup); err != nil {
						return err
					}
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

			providerGroup, ok := lookupGroup(ig.Provider)
			if !ok {
				continue
			}
			if err := resolveGroupReconfigure(ig.Provider, providerGroup); err != nil {
				return err
			}

			// Resolve provider-specific config: custom setup or schema-driven.
			if ig.Setup != nil {
				if err := ig.Setup(); err != nil {
					return err
				}
			} else if ig.Detail != "" {
				detailGroup, ok := lookupGroup(ig.Detail)
				if !ok {
					continue
				}
				if err := resolveGroupReconfigure(ig.Detail, detailGroup); err != nil {
					return err
				}
			}
		}
	}

	ui.Info("Next steps")
	ui.Muted("Run: bosun doctor to verify configuration")
	ui.Muted("Run: bosun start --issue <issue> to begin work")

	return nil
}

// initGroup describes an optional service group for the init wizard.
type initGroup struct {
	Label    string       // Human-readable name for the confirmation prompt.
	Provider string       // Schema group for provider selection (e.g., "issue_tracker").
	Detail   string       // Schema group for provider-specific config (e.g., "jira").
	Setup    func() error // Custom setup flow, replaces resolveGroup(Detail) when set.
}

// serviceInitGroups defines the ordered list of optional service groups
// presented during interactive init.
var serviceInitGroups = []initGroup{
	{Label: "Issue Tracker", Provider: "issue_tracker", Detail: "jira"},
	{Label: "Code Host", Provider: "code_host", Detail: "github"},
	{Label: "Notifications", Provider: "notification", Detail: "slack"},
	{Label: "CI/CD", Provider: "cicd", Detail: "github_actions", Setup: setupGitHubActions},
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
		fmt.Fprintf(&b, "workspace_root: %s\n", wsRoot)
	} else {
		b.WriteString("\n# Uncomment to enable worktree-based workspaces:\n")
		b.WriteString("# workspace_root: .workspaces\n")
	}

	b.WriteString("\n# Uncomment and configure as needed:\n")
	b.WriteString("# jira:\n")
	b.WriteString("#   project: PROJ\n")
	b.WriteString("#\n")
	b.WriteString("# slack:\n")
	b.WriteString("#   channel_review: code-review\n")
	b.WriteString("#   channel_release: releases\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
