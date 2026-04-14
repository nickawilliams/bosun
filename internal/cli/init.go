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

	cmd.Flags().BoolP("interactive", "i", false, "prompt for every value")
	cmd.Flags().Bool("no-detect", false, "skip auto-detection")
	cmd.Flags().Bool("force", false, "overwrite existing .bosun/ directory")
	cmd.Flags().String("workspace-root", "", "where workspaces are created")
	cmd.Flags().StringSlice("repos", nil, "repo glob patterns (e.g. ./* or ~/Projects/myorg/*)")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	rootCard(cmd).Print()
	interactive, _ := cmd.Flags().GetBool("interactive")
	noDetect, _ := cmd.Flags().GetBool("no-detect")
	force, _ := cmd.Flags().GetBool("force")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	bosunDir := filepath.Join(cwd, ".bosun")
	if _, err := os.Stat(bosunDir); err == nil && !force {
		return fmt.Errorf(".bosun/ already exists (use --force to overwrite)")
	}

	// Check if we're inside an existing bosun project.
	if existing := config.FindProjectRoot(); existing != "" && existing != cwd {
		return fmt.Errorf(
			"already inside a bosun project at %s (nested projects are not supported)",
			existing,
		)
	}

	// Resolve repo globs.
	repoGlobs, _ := cmd.Flags().GetStringSlice("repos")
	var detectedGlobs []string
	if len(repoGlobs) == 0 && !noDetect {
		if repos := detectRepos(cwd); len(repos) > 0 {
			ui.CompleteWithDetail("Detected repos", repos)
			detectedGlobs = defaultRepoGlobs(cwd, repos)
		}
	}

	// Resolve workspace_root.
	wsRoot, _ := cmd.Flags().GetString("workspace-root")

	// Prompt for all missing values in a single form.
	needRepos := len(repoGlobs) == 0
	needWS := wsRoot == ""
	if (needRepos || needWS) && interactive && isInteractive() {
		repoInput := strings.Join(detectedGlobs, ", ")
		if repoInput == "" {
			repoInput = "., ./*"
		}
		wsInput := ".workspaces"

		var fields []huh.Field
		if needRepos {
			fields = append(fields, huh.NewInput().
				Title("Repo patterns").
				Description("Comma-separated globs, e.g. ./* or ~/Projects/myorg/*").
				Value(&repoInput))
		}
		if needWS {
			fields = append(fields, huh.NewInput().
				Title("Workspace root").
				Description("Directory where workspaces are created").
				Value(&wsInput))
		}

		rewind := ui.NewCard(ui.CardInput, "Project settings").PrintRewindable()
		if err := runForm(fields...); err != nil {
			return err
		}
		rewind()

		if needRepos {
			for _, g := range strings.Split(repoInput, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repoGlobs = append(repoGlobs, trimmed)
				}
			}
		}
		if needWS {
			wsRoot = wsInput
		}
	}

	// Apply defaults for anything still unresolved.
	if len(repoGlobs) == 0 && len(detectedGlobs) > 0 {
		repoGlobs = detectedGlobs
	}
	if len(repoGlobs) == 0 && !interactive && isInteractive() {
		input, err := promptValue(
			"No repos detected. Enter repo patterns (comma-separated, or leave blank)",
			"")
		if err != nil {
			return err
		}
		if input != "" {
			for _, g := range strings.Split(input, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repoGlobs = append(repoGlobs, trimmed)
				}
			}
		}
	}
	if isDryRun(cmd) {
		ui.DryRun("Would initialize bosun project")
		fields := ui.NewFields(
			"Config", ".bosun/config.yaml",
			"Repos", strings.Join(repoGlobs, ", "),
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

	// Write config.
	configPath := filepath.Join(bosunDir, "config.yaml")
	if err := writeInitConfig(configPath, wsRoot, repoGlobs); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	repoDisplay := strings.Join(repoGlobs, ", ")
	if repoDisplay == "" {
		repoDisplay = "(none — add repo patterns to .bosun/config.yaml)"
	}
	fields := ui.NewFields(
		"Config", configPath,
		"Repos", repoDisplay,
	)
	if wsRoot != "" {
		fields = append(fields, ui.Field{Key: "Workspace root", Value: wsRoot})
	}
	ui.Details("Initialized bosun project", fields)

	ui.Info("Next steps")
	ui.Muted("Edit .bosun/config.yaml to configure Jira, Slack, etc.")
	ui.Muted("Run: bosun start --issue <issue>")

	return nil
}

// detectRepos scans a directory for git repositories: the directory
// itself (if it contains .git/) and immediate children that do.
func detectRepos(dir string) []string {
	var repos []string

	// Check if the directory itself is a repo.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		repos = append(repos, filepath.Base(dir)+" (root)")
	}

	// Check children.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return repos
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
			repos = append(repos, entry.Name())
		}
	}
	return repos
}

// defaultRepoGlobs returns the default repo glob patterns based on
// what was detected. Uses "." for the root repo and "./*" for children.
func defaultRepoGlobs(dir string, detected []string) []string {
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

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// writeInitConfig writes the initial .bosun/config.yaml.
func writeInitConfig(path, wsRoot string, repoGlobs []string) error {
	var b strings.Builder

	b.WriteString("# Repo patterns (globs resolved to directories containing .git/)\n")
	b.WriteString("repos:\n")
	if len(repoGlobs) > 0 {
		for _, g := range repoGlobs {
			fmt.Fprintf(&b, "  - %s\n", g)
		}
	} else {
		b.WriteString("  # - .          # this directory is a repo\n")
		b.WriteString("  # - ./*        # child directories that are repos\n")
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
