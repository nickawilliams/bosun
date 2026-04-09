package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/issue/jira"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/viper"
)

// Repo represents a resolved repository with a short name and absolute path.
type Repo struct {
	Name string // Directory basename, used for worktree directory names.
	Path string // Absolute path to the repo.
}

// resolveRepos expands the repos: globs from config, filters to directories
// containing .git/, and returns the resolved set. If filterNames is non-empty,
// only repos whose names match are returned.
func resolveRepos(filterNames []string) ([]Repo, error) {
	patterns := viper.GetStringSlice("repos")
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no repo patterns configured: set repos in .bosun/config.yaml")
	}

	projectRoot := config.FindProjectRoot()

	var repos []Repo
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		// Resolve relative patterns against project root.
		if !filepath.IsAbs(pattern) && projectRoot != "" {
			pattern = filepath.Join(projectRoot, pattern)
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}

		for _, match := range matches {
			abs, err := filepath.Abs(match)
			if err != nil {
				continue
			}

			// Must be a directory with .git/.
			info, err := os.Stat(abs)
			if err != nil || !info.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
				continue
			}

			name := filepath.Base(abs)
			if seen[abs] {
				continue
			}
			seen[abs] = true

			repos = append(repos, Repo{Name: name, Path: abs})
		}
	}

	if len(filterNames) > 0 {
		filter := make(map[string]bool, len(filterNames))
		for _, n := range filterNames {
			filter[n] = true
		}
		var filtered []Repo
		for _, r := range repos {
			if filter[r.Name] {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf(
				"no repos matched filter %v (available: %s)",
				filterNames, repoNames(repos),
			)
		}
		repos = filtered
	}

	if len(repos) == 0 {
		return nil, fmt.Errorf("no repos found matching configured patterns")
	}

	return repos, nil
}

// newWorkspaceManager creates a workspace.Manager from current config.
func newWorkspaceManager() (*workspace.Manager, error) {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return nil, fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		wsRoot = projectRoot
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	return workspace.NewManager(git.New(), wsRoot), nil
}

// resolveWorkspaceName returns the workspace name from args or auto-detects
// it from CWD.
func resolveWorkspaceName(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		wsRoot = projectRoot
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return workspace.DetectName(wsRoot, cwd)
}

// cliReposToWorkspaceRepos converts CLI Repo types to workspace Repo types.
func cliReposToWorkspaceRepos(repos []Repo) []workspace.Repo {
	result := make([]workspace.Repo, len(repos))
	for i, r := range repos {
		result[i] = workspace.Repo{Name: r.Name, Path: r.Path}
	}
	return result
}

// repoNames returns a comma-separated string of repo names.
func repoNames(repos []Repo) string {
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// newIssueTracker creates an issue.Tracker from current config. Prompts for
// missing values interactively and saves them for future use.
func newIssueTracker() (issue.Tracker, error) {
	provider, err := requireConfig("issue_tracker")
	if err != nil {
		return nil, err
	}

	switch provider {
	case "jira":
		cfg, err := requireGroup("jira")
		if err != nil {
			return nil, err
		}
		return jira.New(cfg["base_url"], cfg["email"], cfg["token"]), nil
	default:
		return nil, fmt.Errorf("unsupported issue tracker: %q", provider)
	}
}

// resolveStatus maps a bosun lifecycle status key (e.g., "in_progress") to
// the provider-specific status name from config (e.g., "In Progress").
func resolveStatus(key string) (string, error) {
	name := viper.GetString("statuses." + key)
	if name == "" {
		return "", fmt.Errorf("status %q not mapped in config statuses section", key)
	}
	return name, nil
}

// validateStageTransition checks the issue's current status against the
// expected status for a lifecycle command. If the status is unexpected, warns
// and prompts for confirmation. In non-interactive mode, logs a warning and
// proceeds.
func validateStageTransition(ctx context.Context, tracker issue.Tracker, issueKey, expectedStatusKey string) error {
	current, err := tracker.GetIssue(ctx, issueKey)
	if err != nil {
		return fmt.Errorf("checking issue status: %w", err)
	}

	expectedStatus, err := resolveStatus(expectedStatusKey)
	if err != nil {
		return err
	}

	if !strings.EqualFold(current.Status, expectedStatus) {
		ui.Warning("Issue %s is in %q, expected %q", issueKey, current.Status, expectedStatus)
		if isInteractive() {
			if !promptConfirm("Proceed anyway?", false) {
				return fmt.Errorf("aborted: unexpected issue status")
			}
		} else {
			ui.Warning("Proceeding (non-interactive mode)")
		}
	}

	return nil
}
