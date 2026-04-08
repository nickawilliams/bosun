package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new bosun project",
		RunE:  runInit,
	}

	cmd.Flags().BoolP("interactive", "I", false, "prompt for every value")
	cmd.Flags().Bool("no-detect", false, "skip auto-detection")
	cmd.Flags().Bool("force", false, "overwrite existing .bosun/ directory")
	cmd.Flags().String("workspace-root", "", "where workspaces are created")
	cmd.Flags().StringSlice("repos", nil, "repo glob patterns (e.g. ./* or ~/Projects/myorg/*)")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
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

	scanner := bufio.NewScanner(os.Stdin)

	// Resolve repo globs.
	repoGlobs, _ := cmd.Flags().GetStringSlice("repos")
	if len(repoGlobs) == 0 {
		detectedGlob := ""
		if !noDetect {
			if repos := detectRepos(cwd); len(repos) > 0 {
				detectedGlob = "./*"
				fmt.Printf("Detected repos: %s\n", strings.Join(repos, ", "))
			}
		}

		if interactive {
			input := prompt(scanner,
				"Repo patterns (comma-separated globs, e.g. ./* or ~/Projects/myorg/*)",
				firstNonEmpty(detectedGlob, "./*"))
			for _, g := range strings.Split(input, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repoGlobs = append(repoGlobs, trimmed)
				}
			}
		} else if detectedGlob != "" {
			repoGlobs = []string{detectedGlob}
		}
	}

	// Prompt for globs if we still have none and aren't in interactive mode.
	if len(repoGlobs) == 0 && !interactive {
		input := prompt(scanner,
			"No repos detected. Enter repo patterns (comma-separated, or leave blank)",
			"")
		if input != "" {
			for _, g := range strings.Split(input, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repoGlobs = append(repoGlobs, trimmed)
				}
			}
		}
	}

	// Resolve workspace_root.
	wsRoot, _ := cmd.Flags().GetString("workspace-root")
	if wsRoot == "" {
		defaultWS := "_workspaces"

		if interactive {
			wsRoot = prompt(scanner,
				"Workspace root (where workspaces are created)",
				defaultWS)
		} else {
			wsRoot = defaultWS
		}
	}

	if isDryRun(cmd) {
		fmt.Println("[dry-run] Would initialize bosun project:")
		fmt.Printf("  .bosun/config.yaml\n")
		fmt.Printf("  repos: %v\n", repoGlobs)
		fmt.Printf("  workspace_root: %s\n", wsRoot)
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

	fmt.Printf("Initialized bosun project in %s\n\n", bosunDir)
	fmt.Printf("  config: %s\n", configPath)
	if len(repoGlobs) > 0 {
		fmt.Printf("  repos:  %s\n", strings.Join(repoGlobs, ", "))
	} else {
		fmt.Printf("  repos:  (none — add repo patterns to .bosun/config.yaml)\n")
	}
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  Edit .bosun/config.yaml to configure Jira, Slack, etc.\n")
	fmt.Printf("  Run: bosun start --issue <issue>\n")

	return nil
}

// detectRepos scans a directory for immediate children that contain a .git
// directory (i.e., are git repositories).
func detectRepos(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var repos []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		gitDir := filepath.Join(dir, entry.Name(), ".git")
		if _, err := os.Stat(gitDir); err == nil {
			repos = append(repos, entry.Name())
		}
	}
	return repos
}

// prompt displays a prompt and returns the user's input, or the default value
// if the user enters nothing.
func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}

	if scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input != "" {
			return input
		}
	}
	return defaultVal
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
		b.WriteString("  # - ./*\n")
		b.WriteString("  # - ~/Projects/myorg/*\n")
	}

	b.WriteString("\n# Where workspaces are created (default: project root)\n")
	fmt.Fprintf(&b, "workspace_root: %s\n", wsRoot)

	b.WriteString("\n# Uncomment and configure as needed:\n")
	b.WriteString("# jira:\n")
	b.WriteString("#   project: PROJ\n")
	b.WriteString("#\n")
	b.WriteString("# slack:\n")
	b.WriteString("#   channel_review: code-review\n")
	b.WriteString("#   channel_release: releases\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
