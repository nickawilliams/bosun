package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check bosun configuration and connectivity",
		Annotations: map[string]string{
			headerAnnotationTitle: "system check",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			r := ui.Default()

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			// Groups organize checks by kind: local environment,
			// project configuration, and external integrations. Each
			// check runs inline; the group's parent spinner provides
			// activity feedback, and the per-check Spinner shows which
			// check is currently in flight.
			groups := []struct {
				label  string
				checks []healthCheck
			}{
				{"environment", environmentChecks()},
				{"project", projectChecks()},
				{"integrations", slices.Concat(
					issueTrackerChecks(),
					codeHostChecks(),
					notificationChecks(),
					cicdChecks(),
				)},
			}

			// Pre-compute the global title alignment width across every
			// check in every group so all rows in the doctor output
			// share the same value column.
			var allChecks []healthCheck
			for _, dg := range groups {
				allChecks = append(allChecks, dg.checks...)
			}
			alignWidth := maxCheckTitleWidth(allChecks)

			passed, warned, failed := 0, 0, 0
			for _, dg := range groups {
				r.Group(dg.label, func(g ui.Reporter) {
					for _, hc := range dg.checks {
						var detail string
						checkErr := g.Spinner(hc.Name, func() error {
							var err error
							detail, err = hc.Check(ctx)
							return err
						})
						emitCheckResult(g, hc, detail, checkErr, alignWidth, &passed, &warned, &failed)
					}
				})
			}

			// Summary card mirrors the project/workspace status summary
			// convention: muted total + comma-joined colored breakdown
			// of non-zero categories. The CardInfo glyph symbol stays
			// the same; its color reflects the aggregate outcome (green
			// when everything passed, warning when anything warned but
			// nothing hard-failed, error when anything failed) so the
			// rollup state is readable from the row glyph alone.
			glyphColor := ui.Palette.Success
			if failed > 0 {
				glyphColor = ui.Palette.Error
			} else if warned > 0 {
				glyphColor = ui.Palette.Warning
			}

			mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
			successStyle := lipgloss.NewStyle().Foreground(ui.Palette.Success)
			warningStyle := lipgloss.NewStyle().Foreground(ui.Palette.Warning)
			errorStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)

			total := passed + warned + failed
			parts := []string{
				mutedStyle.Render(fmt.Sprintf("%d %s", total, pluralize(total, "check", "checks"))),
			}
			if passed > 0 {
				parts = append(parts, successStyle.Render(fmt.Sprintf("%d passed", passed)))
			}
			if warned > 0 {
				parts = append(parts, warningStyle.Render(fmt.Sprintf("%d %s", warned, pluralize(warned, "warning", "warnings"))))
			}
			if failed > 0 {
				parts = append(parts, errorStyle.Render(fmt.Sprintf("%d failed", failed)))
			}
			ui.NewCard(ui.CardInfo, strings.Join(parts, mutedStyle.Render(", "))).
				PreserveCase().
				GlyphColor(glyphColor).
				Print()

			return nil
		},
	}

	addProjectFlag(cmd)
	return cmd
}

// --- Providers ---

// emitCheckResult routes a health check outcome through the group
// Reporter, rendering every state as a one-line `glyph name · detail`
// row so passing, warning, and failing checks share the same visual
// shape. Multi-line details render inline after the separator, with
// continuation lines aligned under the first value character. The
// alignWidth pads every row's title to a common column so the value
// dividers line up across siblings.
func emitCheckResult(g ui.Reporter, hc healthCheck, detail string, checkErr error, alignWidth int, passed, warned, failed *int) {
	if checkErr != nil {
		// Prefer the check's detail when it provided one — lets a
		// check supply a formatted display string alongside an error
		// that just signals the state. Falls back to the error
		// message when no detail is set.
		reason := detail
		if reason == "" {
			reason = checkErr.Error()
		}
		// Required failures get the hard-fail glyph (✗) and counter;
		// anything else — not-required failures and errNotConfigured —
		// counts as a warning and uses the warn glyph (!) so the row's
		// visible state matches the summary count category.
		if hc.Required && !errors.Is(checkErr, errNotConfigured) {
			*failed++
			g.FailValue(hc.Name, reason, alignWidth)
			return
		}
		*warned++
		g.SkipValue(hc.Name, reason, alignWidth)
		return
	}
	*passed++
	if detail == "" {
		g.Complete(hc.Name)
		return
	}
	g.CompleteValue(hc.Name, detail, alignWidth)
}

// maxCheckTitleWidth computes the maximum visual width across the
// title-cased rendering of a check group's names. The result is
// passed as the alignWidth to the Reporter value-form methods so
// every check in the group renders with the same title column.
func maxCheckTitleWidth(checks []healthCheck) int {
	var maxW int
	for _, hc := range checks {
		// titleCase only uppercases fully-lowercase words; visual
		// width is identical to the input string width since no
		// characters are added or removed.
		if w := len(hc.Name); w > maxW {
			maxW = w
		}
	}
	return maxW
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
		{Name: "notification", Check: checkNotification},
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
	group, ok := lookupGroup("issue_tracker.statuses")
	if !ok {
		return "", fmt.Errorf("no status schema defined")
	}

	total := len(group.Keys)
	var mapped int
	var missing []string
	for _, ck := range group.Keys {
		fk := fullKey("issue_tracker.statuses", ck)
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
	provider := viper.GetString("issue_tracker.provider")
	if provider == "" {
		return "", errNotConfigured
	}

	// Validate config completeness.
	switch provider {
	case "jira":
		if group, ok := lookupGroup("issue_tracker"); ok {
			if missing := checkGroupCompleteness("issue_tracker", group); len(missing) > 0 {
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
			baseURL := viper.GetString("issue_tracker.base_url")
			email := viper.GetString("issue_tracker.email")
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

// checkNotification verifies the notification provider end-to-end:
// it's configured, we can authenticate, and each configured channel
// is reachable. The result rolls up into one row — auth confirmation
// on the first line, each channel on its own continuation line —
// with failed channels rendered in the error color. The overall
// glyph reflects the aggregate state: ✓ if auth and all channels
// pass, warn if anything failed (notification isn't a required
// integration, so failures warn rather than fail the whole run).
func checkNotification(ctx context.Context) (string, error) {
	provider := viper.GetString("notification.provider")
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

	// First line: auth confirmation. Continuation lines: one per
	// configured channel; failed probes render in the error color so
	// the per-channel state is visible at a glance.
	errorStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)
	lines := []string{fmt.Sprintf("%s → %s", provider, user)}

	var failedCount int
	for _, key := range []string{"notification.channel_review", "notification.channel_release"} {
		ch := viper.GetString(key)
		if ch == "" {
			continue
		}
		display := "#" + strings.TrimPrefix(ch, "#")
		_, err := notifier.FindThread(notify.WithNoCache(ctx), ch, "__bosun_doctor_probe__")
		if err != nil {
			failedCount++
			lines = append(lines, errorStyle.Render(display))
		} else {
			lines = append(lines, display)
		}
	}

	value := strings.Join(lines, "\n")
	if failedCount > 0 {
		return value, fmt.Errorf("%d %s failed", failedCount, pluralize(failedCount, "channel", "channels"))
	}
	return value, nil
}

func cicdChecks() []healthCheck {
	return []healthCheck{
		{Name: "CI/CD", Check: checkCICD},
	}
}

func checkCICD(ctx context.Context) (string, error) {
	provider := viper.GetString("cicd.provider")
	if provider == "" {
		return "", errNotConfigured
	}
	if _, err := newCICD(); err != nil {
		return "", fmt.Errorf("cannot initialize: %w", err)
	}
	// TODO(arch #28): provider-specific leak. The GitHub Actions
	// adapter shares the code host's token, so cli verifies CI/CD
	// auth by reaching into the code host — and the result string
	// hardcodes "github actions" regardless of which CI/CD provider
	// is configured. cicd.CICD should expose AuthTest returning a
	// display string so the provider owns its auth verification and
	// its identity in the result.
	host, err := newCodeHost()
	if err != nil {
		return "", fmt.Errorf("cannot verify token: %w", err)
	}
	username, err := host.GetAuthenticatedUser(ctx)
	if err != nil {
		return "", fmt.Errorf("auth failed: %w", err)
	}
	return fmt.Sprintf("github actions → %s", username), nil
}
