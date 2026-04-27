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
	preview, err := promptDefault("Preview Workflow", viper.GetString("github_actions.workflows.preview"))
	if err != nil {
		return err
	}
	if preview != "" {
		if err := saveConfigKey("github_actions.workflows.preview", "Preview Workflow", preview); err != nil {
			return err
		}
	} else {
		ui.Skip("Preview Workflow")
	}

	// --- Service input parameter ---
	serviceInput, err := promptDefault("Service Input Parameter", viper.GetString("github_actions.service_input"))
	if err != nil {
		return err
	}
	if serviceInput != "" {
		if err := saveConfigKey("github_actions.service_input", "Service Input Parameter", serviceInput); err != nil {
			return err
		}
	} else {
		ui.Skip("Service Input Parameter")
	}

	// --- Service name mapping ---
	repos, reposErr := resolveRepositories(nil)
	if reposErr == nil && len(repos) > 0 && serviceInput != "" {
		serviceMap := make(map[string]string)
		for _, r := range repos {
			current := viper.GetString("services." + r.Name)
			if current == "" {
				current = r.Name
			}
			val, err := promptDefault(fmt.Sprintf("Service Name for %s", r.Name), current)
			if err != nil {
				return err
			}
			// Only store if different from the default (repo name).
			if val != "" && val != r.Name {
				serviceMap[r.Name] = val
			}
		}

		if len(serviceMap) > 0 {
			for name, svc := range serviceMap {
				viper.Set("services."+name, svc)
			}
			if err := writeNestedConfigMap("", "services", serviceMap); err != nil {
				ui.Skip(fmt.Sprintf("Could not save service mapping: %v", err))
			} else {
				ui.Saved("Services", fmt.Sprintf("%d custom mappings", len(serviceMap)))
			}
		} else {
			ui.Skip("Services (all default to repo name)")
		}
	}

	// --- Release workflow(s) ---
	if reposErr != nil || len(repos) == 0 {
		// No repos configured — prompt for a single release workflow path.
		release, err := promptDefault("Release Workflow", viper.GetString("github_actions.workflows.release"))
		if err != nil {
			return err
		}
		if release != "" {
			if err := saveConfigKey("github_actions.workflows.release", "Release Workflow", release); err != nil {
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

		existing := viper.GetString("github_actions.workflows.release." + r.Name)
		val, err := promptDefault(fmt.Sprintf("Release Workflow for %s", r.Name), existing)
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
			if err := saveConfigKey("github_actions.workflows.release", "Release Workflow", v); err != nil {
				return err
			}
		}
		return nil
	}

	// Multiple repos — save as a YAML map.
	viper.Set("github_actions.workflows.release", releaseMap)
	if err := writeNestedConfigMap("github_actions.workflows", "release", releaseMap); err != nil {
		ui.Skip(fmt.Sprintf("Could not save release workflows: %v", err))
		return nil
	}
	ui.Saved("Release Workflows", fmt.Sprintf("%d repos configured", len(releaseMap)))
	return nil
}

// writeNestedConfigMap writes a map of string key-value pairs as a YAML
// section in the project config file. Parent is a dot-separated path of
// ancestor keys (e.g., "github_actions.workflows"). If parent is empty,
// the key is written at the top level.
func writeNestedConfigMap(parent, key string, values map[string]string) error {
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

	// Walk down parent segments, creating any that don't exist.
	searchFrom := 0
	depth := 0
	if parent != "" {
		for _, seg := range strings.Split(parent, ".") {
			idx := findYAMLKey(lines, seg, searchFrom)
			if idx == -1 {
				indent := strings.Repeat(" ", depth*2)
				newLine := indent + seg + ":"
				if searchFrom > 0 && searchFrom <= len(lines) {
					lines = append(lines[:searchFrom+1], append([]string{newLine}, lines[searchFrom+1:]...)...)
					idx = searchFrom + 1
				} else {
					lines = append(lines, newLine)
					idx = len(lines) - 1
				}
			}
			searchFrom = idx + 1
			depth++
		}
	}

	// Remove any existing key and its children.
	keyIdx := findYAMLKey(lines, key, searchFrom)
	if keyIdx != -1 {
		end := keyIdx + 1
		keyIndent := len(lines[keyIdx]) - len(strings.TrimLeft(lines[keyIdx], " "))
		for end < len(lines) {
			trimmed := strings.TrimLeft(lines[end], " ")
			if trimmed == "" {
				end++
				continue
			}
			indent := len(lines[end]) - len(trimmed)
			if indent <= keyIndent {
				break
			}
			end++
		}
		lines = append(lines[:keyIdx], lines[end:]...)
	}

	// Build the new section.
	baseIndent := depth * 2
	prefix := strings.Repeat(" ", baseIndent)
	childPrefix := strings.Repeat(" ", baseIndent+2)
	var section []string
	section = append(section, fmt.Sprintf("%s%s:", prefix, key))
	for name, val := range values {
		section = append(section, fmt.Sprintf("%s%s: %s", childPrefix, name, val))
	}

	// Insert at end of parent section (or end of file for top-level).
	insertIdx := searchFrom
	for insertIdx < len(lines) {
		trimmed := strings.TrimLeft(lines[insertIdx], " ")
		if trimmed == "" {
			break
		}
		if depth > 0 {
			indent := len(lines[insertIdx]) - len(trimmed)
			if indent < baseIndent {
				break
			}
		}
		insertIdx++
	}

	result := make([]string, 0, len(lines)+len(section))
	result = append(result, lines[:insertIdx]...)
	result = append(result, section...)
	result = append(result, lines[insertIdx:]...)

	return os.WriteFile(configPath, []byte(strings.Join(result, "\n")), 0o644)
}
