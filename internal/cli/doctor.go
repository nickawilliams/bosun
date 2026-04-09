package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check bosun configuration and connectivity",
		RunE: func(cmd *cobra.Command, args []string) error {
			ui.Bold("Bosun Doctor")
			fmt.Println()

			passed := 0
			warned := 0
			failed := 0

			check := func(name string, fn func() (string, error)) {
				detail, err := fn()
				if err != nil {
					ui.Error("%s: %v", name, err)
					failed++
				} else if detail != "" {
					ui.Success("%s: %s", name, detail)
					passed++
				} else {
					ui.Success("%s", name)
					passed++
				}
			}

			warn := func(name string, fn func() (string, error)) {
				detail, err := fn()
				if err != nil {
					ui.Warning("%s: %v", name, err)
					warned++
				} else if detail != "" {
					ui.Success("%s: %s", name, detail)
					passed++
				} else {
					ui.Success("%s", name)
					passed++
				}
			}

			// Config files.
			check("Global config", checkGlobalConfig)
			warn("Project config", checkProjectConfig)

			// Git.
			check("Git", checkGit)

			// Repos.
			warn("Repos", checkRepos)

			// Issue tracker.
			warn("Issue tracker config", checkIssueTrackerConfig)
			if viper.GetString("issue_tracker") != "" {
				warn("Issue tracker connectivity", checkIssueTrackerConnectivity)
			}

			// Status mappings.
			warn("Status mappings", checkStatusMappings)

			// Branch config.
			warn("Branch pattern", checkBranchPattern)

			// Summary.
			fmt.Println()
			summary := fmt.Sprintf("%d passed", passed)
			if warned > 0 {
				summary += fmt.Sprintf(", %d warnings", warned)
			}
			if failed > 0 {
				summary += fmt.Sprintf(", %d failed", failed)
				ui.Error("%s", summary)
			} else if warned > 0 {
				ui.Warning("%s", summary)
			} else {
				ui.Success("%s", summary)
			}

			return nil
		},
	}
}

func checkGlobalConfig() (string, error) {
	dir, err := config.GlobalConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	path := dir + "/config.yaml"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("not found at %s", path)
	}
	return path, nil
}

func checkProjectConfig() (string, error) {
	root := config.FindProjectRoot()
	if root == "" {
		return "", fmt.Errorf("no .bosun/ directory found (run bosun init)")
	}
	path := root + "/.bosun/config.yaml"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("not found at %s", path)
	}
	return path, nil
}

func checkGit() (string, error) {
	path, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("git not found on PATH")
	}
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return path, nil
	}
	return string(out[:len(out)-1]), nil // trim newline
}

func checkRepos() (string, error) {
	repos, err := resolveRepos(nil)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d repos found", len(repos)), nil
}

func checkIssueTrackerConfig() (string, error) {
	provider := viper.GetString("issue_tracker")
	if provider == "" {
		return "", fmt.Errorf("issue_tracker not set in config")
	}

	switch provider {
	case "jira":
		baseURL := viper.GetString("jira.base_url")
		if baseURL == "" {
			return "", fmt.Errorf("jira.base_url not set")
		}
		email := viper.GetString("jira.email")
		if email == "" {
			return "", fmt.Errorf("jira.email not set")
		}
		token := os.Getenv("BOSUN_JIRA_TOKEN")
		if token == "" {
			return "", fmt.Errorf("BOSUN_JIRA_TOKEN env var not set")
		}
		return fmt.Sprintf("%s (%s as %s)", provider, baseURL, email), nil
	default:
		return "", fmt.Errorf("unsupported provider: %q", provider)
	}
}

func checkIssueTrackerConnectivity() (string, error) {
	tracker, err := newIssueTracker()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to fetch a known project's issue to verify auth. Use GetIssue
	// with a dummy key — a 404 means auth works but issue doesn't exist,
	// which is fine. A 401/403 means auth is broken.
	_, err = tracker.GetIssue(ctx, "BOSUN-0")
	if err != nil {
		// Check if it's an auth error vs a "not found" error.
		errStr := err.Error()
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") {
			return "", fmt.Errorf("authentication failed (check BOSUN_JIRA_TOKEN and jira.email)")
		}
		if strings.Contains(errStr, "404") {
			return "authenticated successfully", nil
		}
		// Other errors (DNS, timeout, etc.)
		return "", fmt.Errorf("connection failed: %w", err)
	}

	return "connected and authenticated", nil
}

func checkStatusMappings() (string, error) {
	keys := []string{"ready", "in_progress", "review", "preview", "ready_for_release", "done"}
	var missing []string
	var mapped int
	for _, k := range keys {
		if viper.GetString("statuses."+k) != "" {
			mapped++
		} else {
			missing = append(missing, k)
		}
	}

	if mapped == 0 {
		return "", fmt.Errorf("no status mappings configured (add statuses section to config)")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%d/%d mapped (missing: %s)", mapped, len(keys), strings.Join(missing, ", "))
	}
	return fmt.Sprintf("%d/%d mapped", mapped, len(keys)), nil
}

func checkBranchPattern() (string, error) {
	pattern := viper.GetString("branch.pattern")
	if pattern == "" {
		return "using default ({{.Category}}/{{.IssueNumber}}_{{.IssueSlug}})", nil
	}
	return pattern, nil
}

