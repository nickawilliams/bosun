package cli

import (
	"fmt"

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
		ui.Skip("preview workflow")
	}

	// --- Service input parameter ---
	serviceInputDefault := viper.GetString("github_actions.service_input")
	if serviceInputDefault == "" {
		// Matches schema default in schema.go. The config schema refactor
		// (ROADMAP.md) will unify these into a single source of truth.
		serviceInputDefault = "services-to-deploy"
	}
	serviceInput, err := promptDefault("Service Input Parameter", serviceInputDefault)
	if err != nil {
		return err
	}
	if serviceInput != "" {
		if err := saveConfigKey("github_actions.service_input", "Service Input Parameter", serviceInput); err != nil {
			return err
		}
	} else {
		ui.Skip("service input parameter")
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
			configPath, cpErr := configPathForScope(false)
			if cpErr != nil {
				ui.Skip(fmt.Sprintf("could not save service mapping: %v", cpErr))
			} else if err := setConfigMap(configPath, "services", serviceMap); err != nil {
				ui.Skip(fmt.Sprintf("could not save service mapping: %v", err))
			} else {
				ui.Saved("services", fmt.Sprintf("%d custom mappings", len(serviceMap)))
			}
		} else {
			ui.Skip("services (all default to repo name)")
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
			ui.Skip("release workflow")
		}
		return nil
	}

	// Repos available — prompt per repo.
	releaseMap := make(map[string]string)

	for _, r := range repos {
		existing := viper.GetString("github_actions.workflows.release." + r.Name)
		if existing == "" {
			existing = ".github/workflows/production.yml"
		}
		val, err := promptDefault(fmt.Sprintf("Release Workflow for %s", r.Name), existing)
		if err != nil {
			return err
		}
		if val != "" {
			releaseMap[r.Name] = val
		}
	}

	if len(releaseMap) == 0 {
		ui.Skip("release workflows")
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
	configPath, err := configPathForScope(false)
	if err != nil {
		ui.Skip(fmt.Sprintf("could not save release workflows: %v", err))
		return nil
	}
	if err := setConfigMap(configPath, "github_actions.workflows.release", releaseMap); err != nil {
		ui.Skip(fmt.Sprintf("could not save release workflows: %v", err))
		return nil
	}
	ui.Saved("release workflows", fmt.Sprintf("%d repos configured", len(releaseMap)))
	return nil
}
