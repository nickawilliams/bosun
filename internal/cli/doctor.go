package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// healthCheck describes a single diagnostic check.
type healthCheck struct {
	Name     string
	Required bool // fail vs warn on error
	Check    func(ctx context.Context) (string, error)
}

// errNotConfigured is returned by health checks to indicate a service
// is absent (not an error). The doctor renders this as a warning (!)
// rather than a failure (✗).
var errNotConfigured = fmt.Errorf("(not configured)")

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check bosun configuration and connectivity",
		Annotations: map[string]string{
			headerAnnotationTitle: "system check",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCard(cmd).Print()
			r := ui.Default()

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Groups mirror MODEL.md phases: environment, project,
			// integrations, CI/CD. Each check runs inline; the
			// group's parent spinner provides activity feedback.
			groups := []struct {
				label  string
				checks []healthCheck
			}{
				{"environment", environmentChecks()},
				{"project", projectChecks()},
				{"integrations", append(append(
					issueTrackerChecks(), codeHostChecks()...), notificationChecks()...)},
				{"CI/CD", cicdChecks()},
			}

			passed, warned, failed := 0, 0, 0
			for _, dg := range groups {
				r.Group(dg.label, func(g ui.Reporter) {
					for _, hc := range dg.checks {
						detail, checkErr := hc.Check(ctx)
						emitCheckResult(g, hc, detail, checkErr, &passed, &warned, &failed)
					}
				})
			}

			// Summary.
			parts := []string{fmt.Sprintf("%d passed", passed)}
			if warned > 0 {
				parts = append(parts, fmt.Sprintf("%d warnings", warned))
			}
			if failed > 0 {
				parts = append(parts, fmt.Sprintf("%d failed", failed))
			}
			r.Info("%s", strings.Join(parts, ", "))

			return nil
		},
	}
}

// --- Providers ---

// emitCheckResult routes a health check outcome through the group
// Reporter. Detail formatting matches the spec's inline style
// (e.g. "tracker: authenticated as nick", "code host: auth failed").
func emitCheckResult(g ui.Reporter, hc healthCheck, detail string, checkErr error, passed, warned, failed *int) {
	if checkErr != nil {
		label := fmt.Sprintf("%s: %s", hc.Name, checkErr.Error())
		if errors.Is(checkErr, errNotConfigured) {
			*warned++
			g.Skip(label)
			return
		}
		if hc.Required {
			*failed++
		} else {
			*warned++
		}
		g.Fail(label)
		return
	}
	*passed++
	if detail == "" {
		g.Complete(hc.Name)
		return
	}
	if strings.Contains(detail, "\n") {
		g.CompleteDetail(hc.Name, strings.Split(detail, "\n"))
	} else {
		g.Selected(hc.Name, detail)
	}
}

func environmentChecks() []healthCheck {
	return []healthCheck{
		{Name: "global config", Required: true, Check: checkGlobalConfig},
		{Name: "project config", Check: checkProjectConfig},
		{Name: "git", Required: true, Check: checkGit},
	}
}

func projectChecks() []healthCheck {
	return []healthCheck{
		{Name: "repositories", Check: checkRepositories},
		{Name: "branch template", Check: checkBranchTemplate},
		{Name: "status mappings", Check: checkStatusMappings},
	}
}

func issueTrackerChecks() []healthCheck {
	return []healthCheck{
		{Name: "issue tracker", Check: checkIssueTracker},
	}
}

func codeHostChecks() []healthCheck {
	return []healthCheck{
		{Name: "code host", Check: checkCodeHost},
	}
}

func notificationChecks() []healthCheck {
	return []healthCheck{
		{Name: "notification config", Check: checkNotificationConfig},
		{Name: "notification auth", Check: checkNotificationAuth},
		{Name: "notification channels", Check: checkNotificationChannels},
	}
}

// --- Check implementations ---

func checkGlobalConfig(_ context.Context) (string, error) {
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

func checkProjectConfig(_ context.Context) (string, error) {
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

func checkGit(_ context.Context) (string, error) {
	_, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("not found on PATH")
	}
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return "found", nil
	}
	ver := strings.TrimSpace(string(out))
	ver = strings.TrimPrefix(ver, "git version ")
	return ver, nil
}

func checkRepositories(_ context.Context) (string, error) {
	repositories, err := resolveRepositories(nil)
	if err != nil {
		return "", err
	}
	names := make([]string, len(repositories))
	for i, r := range repositories {
		names[i] = r.Name
	}
	return strings.Join(names, "\n"), nil
}

func checkBranchTemplate(_ context.Context) (string, error) {
	pattern := viper.GetString("branch.template")
	if pattern == "" {
		return "default", nil
	}
	return pattern, nil
}

func checkStatusMappings(_ context.Context) (string, error) {
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

func checkIssueTracker(ctx context.Context) (string, error) {
	provider := viper.GetString("issue_tracker")
	if provider == "" {
		return "", errNotConfigured
	}

	// Validate config completeness.
	switch provider {
	case "jira":
		if group, ok := lookupGroup("jira"); ok {
			if missing := checkGroupCompleteness("jira", group); len(missing) > 0 {
				return "", fmt.Errorf("missing: %s", strings.Join(missing, ", "))
			}
		}
	default:
		return "", fmt.Errorf("unsupported: %q", provider)
	}

	// Test connectivity.
	tracker, err := newIssueTracker()
	if err != nil {
		return "", err
	}

	_, err = tracker.GetIssue(ctx, "BOSUN-0")
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "401") || strings.Contains(errStr, "403") {
			return "", fmt.Errorf("auth failed (check token and email)")
		}
		if strings.Contains(errStr, "404") {
			// 404 means we authenticated but the issue doesn't exist — that's fine.
			baseURL := viper.GetString("jira.base_url")
			email := viper.GetString("jira.email")
			host := strings.TrimPrefix(baseURL, "https://")
			host = strings.TrimPrefix(host, "http://")
			host = strings.TrimRight(host, "/")
			return fmt.Sprintf("%s → %s (%s)", provider, host, email), nil
		}
		return "", fmt.Errorf("connection failed: %w", err)
	}

	return provider + " · authenticated", nil
}

func checkCodeHost(ctx context.Context) (string, error) {
	host, err := newCodeHost()
	if err != nil {
		return "", errNotConfigured
	}

	username, err := host.GetAuthenticatedUser(ctx)
	if err != nil {
		return "", fmt.Errorf("auth failed: %w", err)
	}

	return fmt.Sprintf("github → %s", username), nil
}

func checkNotificationConfig(_ context.Context) (string, error) {
	provider := viper.GetString("notification")
	if provider == "" {
		return "", errNotConfigured
	}

	auth := viper.GetString("slack.auth")
	if auth == "" {
		auth = "token"
	}

	detail := provider + " (auth: " + auth + ")"
	if auth == "local" {
		ws := viper.GetString("slack.workspace")
		if ws == "" {
			return "", fmt.Errorf("slack.workspace not set (required for local auth)")
		}
		detail += "\nworkspace: " + ws
	}

	var channels []string
	if ch := viper.GetString("slack.channel_review"); ch != "" {
		channels = append(channels, "#"+strings.TrimPrefix(ch, "#"))
	}
	if ch := viper.GetString("slack.channel_release"); ch != "" {
		channels = append(channels, "#"+strings.TrimPrefix(ch, "#"))
	}
	if len(channels) > 0 {
		detail += "\nchannels: " + strings.Join(channels, ", ")
	} else {
		detail += "\nno channels configured"
	}

	return detail, nil
}

func checkNotificationAuth(ctx context.Context) (string, error) {
	provider := viper.GetString("notification")
	if provider == "" {
		return "", errNotConfigured
	}

	notifier, err := newNotifier()
	if err != nil {
		return "", err
	}
	defer notifier.Close()

	user, err := notifier.AuthTest(notify.WithNoCache(ctx))
	if err != nil {
		return "", fmt.Errorf("auth failed: %w", err)
	}

	return fmt.Sprintf("authenticated as %s", user), nil
}

func checkNotificationChannels(ctx context.Context) (string, error) {
	provider := viper.GetString("notification")
	if provider == "" {
		return "", errNotConfigured
	}

	notifier, err := newNotifier()
	if err != nil {
		return "", err
	}
	defer notifier.Close()

	var results []string
	for _, key := range []string{"slack.channel_review", "slack.channel_release"} {
		ch := viper.GetString(key)
		if ch == "" {
			continue
		}
		// FindThread with a bogus issue key just to exercise channel resolution.
		display := "#" + strings.TrimPrefix(ch, "#")
		_, err := notifier.FindThread(notify.WithNoCache(ctx), ch, "__bosun_doctor_probe__")
		if err != nil {
			results = append(results, fmt.Sprintf("%s ✗ %v", display, err))
		} else {
			results = append(results, fmt.Sprintf("%s ✓", display))
		}
	}

	if len(results) == 0 {
		return "", fmt.Errorf("no channels configured")
	}

	return strings.Join(results, "\n"), nil
}

func cicdChecks() []healthCheck {
	return []healthCheck{
		{Name: "CI/CD config", Check: checkCICDConfig},
		{Name: "CI/CD auth", Check: checkCICDAuth},
	}
}

func checkCICDConfig(_ context.Context) (string, error) {
	provider := viper.GetString("cicd")
	if provider == "" {
		return "", errNotConfigured
	}

	var details []string
	details = append(details, "provider: "+provider)

	if up := viper.GetString("github_actions.workflows.preview.up.target"); up != "" {
		details = append(details, "preview up: "+up)
	}

	if down := viper.GetString("github_actions.workflows.preview.down.target"); down != "" {
		details = append(details, "preview down: "+down)
	}

	release := viper.Get("github_actions.workflows.release.target")
	switch v := release.(type) {
	case string:
		details = append(details, "release: "+v)
	case map[string]any:
		details = append(details, fmt.Sprintf("release: %d repos configured", len(v)))
	}

	if input := stageInputName("preview.up", "services"); input != "" {
		details = append(details, "service input: "+input)
	}

	return strings.Join(details, "\n"), nil
}

func checkCICDAuth(ctx context.Context) (string, error) {
	provider := viper.GetString("cicd")
	if provider == "" {
		return "", errNotConfigured
	}

	pipeline, err := newCICD()
	if err != nil {
		return "", fmt.Errorf("cannot initialize: %w", err)
	}

	// Use the code host to verify the GitHub token works, since the
	// CI/CD adapter uses the same token.
	host, err := newCodeHost()
	if err != nil {
		return "", fmt.Errorf("cannot verify token: %w", err)
	}

	username, err := host.GetAuthenticatedUser(ctx)
	if err != nil {
		return "", fmt.Errorf("auth failed: %w", err)
	}

	_ = pipeline // validated by newCICD succeeding
	return fmt.Sprintf("github actions → %s", username), nil
}
