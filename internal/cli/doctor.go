package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
		RunE: func(cmd *cobra.Command, args []string) error {
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
	passStyle := lipgloss.NewStyle().Foreground(ui.Palette.Success)
	warnStyle := lipgloss.NewStyle().Foreground(ui.Palette.Warning)
	failStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)
	nameStyle := lipgloss.NewStyle().Bold(true)
	detailStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

	ui.Bold("Bosun Doctor")
	fmt.Println()

	passed, warned, failed := 0, 0, 0

	for _, r := range results {
		var symbol string
		switch r.status {
		case "pass":
			symbol = passStyle.Render(ui.Palette.Check)
			passed++
		case "warn":
			symbol = warnStyle.Render("!")
			warned++
		case "fail":
			symbol = failStyle.Render(ui.Palette.Cross)
			failed++
		}

		name := nameStyle.Render(r.name)
		if r.detail != "" {
			detail := detailStyle.Render(r.detail)
			fmt.Printf("  %s  %-24s %s\n", symbol, name, detail)
		} else {
			fmt.Printf("  %s  %s\n", symbol, name)
		}
	}

	// Summary line.
	fmt.Println()
	parts := []string{passStyle.Render(fmt.Sprintf("%d passed", passed))}
	if warned > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("%d warnings", warned)))
	}
	if failed > 0 {
		parts = append(parts, failStyle.Render(fmt.Sprintf("%d failed", failed)))
	}
	fmt.Printf("  %s\n", strings.Join(parts, "  "))
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
	return strings.Join(names, ", "), nil
}

func checkIssueTrackerConfig() (string, error) {
	provider := viper.GetString("issue_tracker")
	if provider == "" {
		return "", fmt.Errorf("not configured")
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
			return "", fmt.Errorf("BOSUN_JIRA_TOKEN not set")
		}
		// Trim URL to just the hostname for display.
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
		return "", fmt.Errorf("none configured")
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("%d/%d (missing: %s)", mapped, len(keys), strings.Join(missing, ", "))
	}
	return fmt.Sprintf("%d/%d", mapped, len(keys)), nil
}

func checkBranchPattern() (string, error) {
	pattern := viper.GetString("branch.pattern")
	if pattern == "" {
		return "default", nil
	}
	return pattern, nil
}
