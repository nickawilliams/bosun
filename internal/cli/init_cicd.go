package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// setupGitHubActions guides the user through configuring GitHub Actions
// workflows for preview and release stages.
func setupGitHubActions() error {
	// --- Preview workflow ---
	preview, err := promptValue("Preview Workflow", "")
	if err != nil {
		return err
	}
	if preview != "" {
		if err := saveConfigKey("github_actions.preview", "Preview Workflow", preview); err != nil {
			return err
		}
	} else {
		ui.Skip("Preview Workflow")
	}

	// --- Release workflow(s) ---
	repos, reposErr := resolveRepositories(nil)
	if reposErr != nil || len(repos) == 0 {
		// No repos configured — prompt for a single release workflow path.
		release, err := promptValue("Release Workflow", "")
		if err != nil {
			return err
		}
		if release != "" {
			if err := saveConfigKey("github_actions.release", "Release Workflow", release); err != nil {
				return err
			}
		} else {
			ui.Skip("Release Workflow")
		}
		return nil
	}

	// Repos available — prompt per repo.
	ctx := context.Background()
	releaseMap := make(map[string]string)

	for _, r := range repos {
		// Build a helpful default using the repo's remote identity.
		example := ".github/workflows/deploy.yml"
		if identity, err := gh.ParseRemote(ctx, r.Path); err == nil {
			example = fmt.Sprintf("%s/%s/.github/workflows/deploy.yml", identity.Owner, identity.Name)
		}

		val, err := promptValue(fmt.Sprintf("Release Workflow for %s", r.Name), "")
		if err != nil {
			return err
		}
		if val != "" {
			releaseMap[r.Name] = val
		}
		_ = example // shown in prompt context but not enforced
	}

	if len(releaseMap) == 0 {
		ui.Skip("Release Workflows")
		return nil
	}

	// Single repo — save as a plain string.
	if len(releaseMap) == 1 {
		for _, v := range releaseMap {
			if err := saveConfigKey("github_actions.release", "Release Workflow", v); err != nil {
				return err
			}
		}
		return nil
	}

	// Multiple repos — save as a YAML map.
	viper.Set("github_actions.release", releaseMap)
	if err := writeReleaseWorkflows(releaseMap); err != nil {
		ui.Skip(fmt.Sprintf("Could not save release workflows: %v", err))
		return nil
	}
	ui.Saved("Release Workflows", fmt.Sprintf("%d repos configured", len(releaseMap)))
	return nil
}

// writeReleaseWorkflows writes the github_actions.release map section
// to the project config file.
func writeReleaseWorkflows(workflows map[string]string) error {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return fmt.Errorf("not inside a bosun project")
	}
	configPath := projectRoot + "/.bosun/config.yaml"

	content, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	lines := strings.Split(string(content), "\n")

	// Find the github_actions: parent.
	parentIdx := findYAMLKey(lines, "github_actions", 0)
	if parentIdx == -1 {
		// Append the entire section.
		lines = append(lines, "github_actions:")
		parentIdx = len(lines) - 1
	}

	// Remove any existing "release:" under github_actions.
	releaseIdx := findYAMLKey(lines, "release", parentIdx+1)
	if releaseIdx != -1 {
		// Remove the release key and any indented children.
		end := releaseIdx + 1
		for end < len(lines) {
			trimmed := strings.TrimLeft(lines[end], " ")
			if trimmed == "" || strings.Repeat(" ", 4) <= lines[end][:len(lines[end])-len(trimmed)] {
				// Still indented deeper than release level — keep scanning.
				indent := len(lines[end]) - len(trimmed)
				releaseIndent := len(lines[releaseIdx]) - len(strings.TrimLeft(lines[releaseIdx], " "))
				if indent > releaseIndent {
					end++
					continue
				}
			}
			break
		}
		lines = append(lines[:releaseIdx], lines[end:]...)
	}

	// Build the new release section.
	var section []string
	section = append(section, "  release:")
	for name, workflow := range workflows {
		section = append(section, fmt.Sprintf("    %s: %s", name, workflow))
	}

	// Insert after github_actions parent (and any existing keys before it).
	insertIdx := parentIdx + 1
	// Skip past other github_actions children to append at end of section.
	for insertIdx < len(lines) {
		trimmed := strings.TrimLeft(lines[insertIdx], " ")
		if trimmed == "" {
			break
		}
		indent := len(lines[insertIdx]) - len(trimmed)
		if indent < 2 {
			break // No longer inside github_actions
		}
		insertIdx++
	}

	result := make([]string, 0, len(lines)+len(section))
	result = append(result, lines[:insertIdx]...)
	result = append(result, section...)
	result = append(result, lines[insertIdx:]...)

	return os.WriteFile(configPath, []byte(strings.Join(result, "\n")), 0o644)
}
