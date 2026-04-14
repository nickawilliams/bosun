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

type checkResult struct {
	name   string
	status string // "pass", "warn", "fail"
	detail string
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check bosun configuration and connectivity",
		Annotations: map[string]string{
			headerAnnotationTitle: "system check",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCard(cmd).Print()
			var results []checkResult

			check := func(name string, required bool, fn func() (string, error)) {
				detail, err := fn()
				if err != nil {
					status := "warn"
					if required {
						status = "fail"
					}
					results = append(results, checkResult{name, status, err.Error()})
				} else {
					results = append(results, checkResult{name, "pass", detail})
				}
			}

			// Configuration.
			check("Global config", true, checkGlobalConfig)
			check("Project config", false, checkProjectConfig)

			// Tools.
			check("Git", true, checkGit)

			// Project.
			check("Repos", false, checkRepos)
			check("Branch pattern", false, checkBranchPattern)
			check("Status mappings", false, checkStatusMappings)

			// Issue tracker.
			check("Issue tracker", false, checkIssueTrackerConfig)
			if viper.GetString("issue_tracker") != "" {
				check("Tracker auth", false, checkIssueTrackerConnectivity)
			}

			// Render.
			renderDoctorResults(results)

			return nil
		},
	}
}

func renderDoctorResults(results []checkResult) {
	passed, warned, failed := 0, 0, 0

	for _, r := range results {
		detail := strings.Split(r.detail, "\n")
		switch r.status {
		case "pass":
			passed++
			if r.detail != "" {
				ui.CompleteWithDetail(r.name, detail)
			} else {
				ui.Complete(r.name)
			}
		case "warn":
			warned++
			if r.detail != "" {
				ui.Skip(fmt.Sprintf("%s: %s", r.name, r.detail))
			} else {
				ui.Skip(r.name)
			}
		case "fail":
			failed++
			if r.detail != "" {
				ui.Fail(fmt.Sprintf("%s: %s", r.name, r.detail))
			} else {
				ui.Fail(r.name)
			}
		}
	}

	// Summary.
	parts := []string{fmt.Sprintf("%d passed", passed)}
	if warned > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", warned))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	ui.Info("%s", strings.Join(parts, ", "))
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
		return "", fmt.Errorf("no .bosun/ found (run bosun init)")
	}
	path := root + "/.bosun/config.yaml"
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("not found at %s", path)
	}
	return path, nil
}

func checkGit() (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("not found on PATH")
	}
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return "found", nil
	}
	// Extract just the version number from "git version 2.50.1"
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "git version ")
	return ver, nil
}

func checkRepos() (string, error) {
	repos, err := resolveRepos(nil)
	if err != nil {
		return "", err
	}
	names := make([]string, len(repos))
	for i, r := range repos {
		names[i] = r.Name
	}
	return strings.Join(names, "\n"), nil
}

func checkIssueTrackerConfig() (string, error) {
	if group, ok := lookupGroup("issue_tracker"); ok {
		if missing := checkGroupCompleteness("issue_tracker", group); len(missing) > 0 {
			return "", fmt.Errorf("not configured")
		}
	}

	provider := viper.GetString("issue_tracker")
	switch provider {
	case "jira":
		if group, ok := lookupGroup("jira"); ok {
			if missing := checkGroupCompleteness("jira", group); len(missing) > 0 {
				return "", fmt.Errorf("missing: %s", strings.Join(missing, ", "))
			}
		}
		baseURL := viper.GetString("jira.base_url")
		email := viper.GetString("jira.email")
		host := strings.TrimPrefix(baseURL, "https://")
		host = strings.TrimPrefix(host, "http://")
		host = strings.TrimRight(host, "/")
		return fmt.Sprintf("jira → %s (%s)", host, email), nil
	default:
		return "", fmt.Errorf("unsupported: %q", provider)
	}
}

func checkIssueTrackerConnectivity() (string, error) {
	tracker, err := newIssueTracker()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = tracker.GetIssue(ctx, "BOSUN-0")
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") {
			return "", fmt.Errorf("auth failed (check token and email)")
		}
		if strings.Contains(errStr, "404") {
			return "authenticated", nil
		}
		return "", fmt.Errorf("connection failed: %w", err)
	}

	return "connected", nil
}

func checkStatusMappings() (string, error) {
	group, ok := lookupGroup("statuses")
	if !ok {
		return "", fmt.Errorf("no status schema defined")
	}

	total := len(group.Keys)
	var mapped int
	var missing []string
	for _, ck := range group.Keys {
		fk := fullKey("statuses", ck)
		if viper.GetString(fk) != "" || ck.Default != "" {
			mapped++
		} else {
			missing = append(missing, ck.Key)
		}
	}

	if mapped == 0 {
		return "", fmt.Errorf("none configured")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%d/%d (missing: %s)", mapped, total, strings.Join(missing, ", "))
	}
	return fmt.Sprintf("%d/%d", mapped, total), nil
}

func checkBranchPattern() (string, error) {
	pattern := viper.GetString("branch.pattern")
	if pattern == "" {
		return "default", nil
	}
	return pattern, nil
}
