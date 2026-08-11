package cli_test

// End-to-end scenarios for `bosun doctor` through the test harness.
// doctor is the diagnostic users consult when an integration is
// broken: it walks every configured capability and reports whether
// config + auth are correct for each. It mutates nothing, so every
// assertion here is on what the command *reported* — the per-check
// row (state, name, value) and the summary rollup.
//
// Three things about the implementation as built shape these tests,
// and each diverges from the scenario tree sketched in issue #24:
//
//  1. Under the harness, output is a buffer rather than a TTY, so
//     Bootstrap installs a raw Reporter and the rendered timeline
//     never reaches h.Stdout(). Assertions go through
//     h.Reporter (ui.CaptureReporter), which records the semantic
//     Reporter calls — the same information the glyphs encode,
//     without parsing styled terminal output.
//
//  2. None of the integration checks are Required, so a broken
//     provider warns (!) rather than fails (✗). The ✗ glyph is
//     reserved for the required environment checks (global config,
//     git). doctor distinguishes two warn shapes and the tests assert
//     on which one appears: "(not configured)" for an absent
//     integration versus "configured but failing — …" for one that is
//     set up but broken. Conflating them is the bug these scenarios
//     guard against.
//
//  3. doctor always exits 0 — it reports state, it doesn't gate on
//     it. The exit_code scenarios pin that contract rather than the
//     non-zero-on-failure behavior the issue assumed; see the group's
//     comment.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

// doctorConfig is a fully-configured project: every capability
// doctor probes has its provider (and the keys that provider's schema
// marks required) set, so the baseline run reports success on all
// four integrations. Scenarios that want one capability absent
// rewrite the config without that block.
const doctorConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  provider: "jira"
  base_url: "https://acme.atlassian.net"
  email: "dev@acme.test"
  token: "jira-token"
  project: "EX"
code_host:
  provider: "github"
  token: "gh-token"
notification:
  provider: "slack"
  token: "slack-token"
  channel_review: "reviews"
  channel_prerelease: "releases"
cicd:
  provider: "github_actions"
`

// doctorProbeIssue is the issue key doctor fetches to verify tracker
// connectivity. Seeding it is what makes the tracker probe succeed;
// a tracker that returns 404 for it is also healthy (see the
// tracker_probe scenarios), which is why the real key is arbitrary.
const doctorProbeIssue = "BOSUN-0"

// Check names, as doctor labels its rows.
const (
	rowGlobalConfig   = "global config"
	rowProjectConfig  = "project config"
	rowGit            = "git"
	rowRepositories   = "repositories"
	rowBranchTemplate = "branch template"
	rowStatusMappings = "status mappings"
	rowTracker        = "issue tracker"
	rowCodeHost       = "code host"
	rowNotification   = "notification"
	rowCICD           = "CI/CD"
)

// notConfigured is the reason doctor renders for an integration whose
// provider isn't set at all — distinct from one that is configured
// and failing.
const notConfigured = "(not configured)"

// withoutConfig returns doctorConfig with snippet removed — how the
// unconfigured scenarios drop one capability from the fully
// configured baseline. It fails the test when snippet isn't present,
// so a drifted baseline surfaces as a broken helper rather than a
// scenario that silently tests the configured case.
func withoutConfig(t *testing.T, snippet string) string {
	t.Helper()
	if !strings.Contains(doctorConfig, snippet) {
		t.Fatalf("doctorConfig no longer contains %q", snippet)
	}
	return strings.Replace(doctorConfig, snippet, "", 1)
}

// newDoctorHarness builds the healthy baseline every scenario starts
// from: a global config on disk (the required global-config check
// looks for the file), a fully-configured project with one repo, the
// CI/CD fake installed, and the tracker's connectivity probe issue
// seeded.
func newDoctorHarness(t *testing.T) *testharness.Harness {
	t.Helper()
	h := testharness.New(t)
	h.WriteGlobalConfig("# bosun global config\n")
	h.Workspace.WriteConfig(doctorConfig)
	h.Workspace.AddRepo("api")
	h.InstallCICD()
	h.Tracker.SeedIssue(issue.Issue{
		Key: doctorProbeIssue, Title: "connectivity probe", Type: "Task",
	})
	return h
}

// runDoctor executes the command and fails the test if it errors —
// doctor reports problems rather than returning them, so a non-nil
// error means the run itself broke (bad flags, unreadable project),
// not that a check failed.
func runDoctor(t *testing.T, h *testharness.Harness) {
	t.Helper()
	if err := h.Run("doctor"); err != nil {
		t.Fatalf("doctor: %v\n%s", err, h.Reporter.Dump())
	}
}

// doctorRow returns the reported row for the named check. Values are
// ANSI-stripped: doctor colors sub-parts of some values (failed
// notification channels), and the styling is not what these tests are
// pinning.
func doctorRow(t *testing.T, h *testharness.Harness, name string) ui.CaptureEvent {
	t.Helper()
	ev, ok := h.Reporter.Find(name)
	if !ok {
		t.Fatalf("no %q row reported\n%s", name, h.Reporter.Dump())
	}
	ev.Value = ansi.Strip(ev.Value)
	return ev
}

// assertRow asserts a check reported the given state (one of the
// ui.Capture* kinds) with a value containing want.
func assertRow(t *testing.T, h *testharness.Harness, name, kind, want string) {
	t.Helper()
	ev := doctorRow(t, h, name)
	if ev.Kind != kind {
		t.Errorf("%s: state = %q, want %q (value %q)", name, ev.Kind, kind, ev.Value)
	}
	if want != "" && !strings.Contains(ev.Value, want) {
		t.Errorf("%s: value = %q, want it to contain %q", name, ev.Value, want)
	}
}

// assertPassed / assertWarned / assertFailed are the three states a
// doctor row can report: ✓, !, and ✗ respectively.
func assertPassed(t *testing.T, h *testharness.Harness, name, want string) {
	t.Helper()
	assertRow(t, h, name, ui.CaptureComplete, want)
}

func assertWarned(t *testing.T, h *testharness.Harness, name, want string) {
	t.Helper()
	assertRow(t, h, name, ui.CaptureSkip, want)
}

func assertFailed(t *testing.T, h *testharness.Harness, name, want string) {
	t.Helper()
	assertRow(t, h, name, ui.CaptureFail, want)
}

// assertNotConfigured asserts a check reported the absent-integration
// warning, and *only* that.
//
// The exact match is the point. "configured but failing — (not
// configured)" contains the not-configured reason as a substring, so a
// containment assertion would accept the very conflation these
// scenarios exist to catch: doctor losing the errNotConfigured branch
// and rendering every absent integration as a broken one.
func assertNotConfigured(t *testing.T, h *testharness.Harness, name string) {
	t.Helper()
	ev := doctorRow(t, h, name)
	if ev.Kind != ui.CaptureSkip {
		t.Errorf("%s: state = %q, want %q (value %q)", name, ev.Kind, ui.CaptureSkip, ev.Value)
	}
	if ev.Value != notConfigured {
		t.Errorf("%s: value = %q, want exactly %q — an absent integration must not "+
			"read as a broken one", name, ev.Value, notConfigured)
	}
}

// assertHealthyExcept asserts every integration check passed except
// the ones named — the "failure is reported for this provider and
// only this provider" assertion the per-capability scenarios need.
func assertHealthyExcept(t *testing.T, h *testharness.Harness, except ...string) {
	t.Helper()
	skip := map[string]bool{}
	for _, name := range except {
		skip[name] = true
	}
	for _, name := range []string{rowTracker, rowCodeHost, rowNotification, rowCICD} {
		if skip[name] {
			continue
		}
		ev := doctorRow(t, h, name)
		if ev.Kind != ui.CaptureComplete {
			t.Errorf("%s: state = %q, want %q — should be unaffected (value %q)",
				name, ev.Kind, ui.CaptureComplete, ev.Value)
		}
	}
}

// doctorSummary returns the passed / warned / failed counts from the
// summary card. doctor orders the segments ascending by severity so
// the worst non-zero one colors the card; the tests read them
// positionally and assert that ordering holds.
func doctorSummary(t *testing.T, h *testharness.Harness) (passed, warned, failed int) {
	t.Helper()
	events := h.Reporter.OfKind(ui.CaptureSummary)
	if len(events) != 1 {
		t.Fatalf("got %d summary cards, want 1\n%s", len(events), h.Reporter.Dump())
	}
	segments := events[0].Segments
	if len(segments) != 3 {
		t.Fatalf("summary has %d segments, want 3 (passed, warnings, failed)", len(segments))
	}
	if got := segments[0].Label; got != "passed" {
		t.Errorf("summary segment 0 = %q, want %q", got, "passed")
	}
	if got := segments[2].Label; got != "failed" {
		t.Errorf("summary segment 2 = %q, want %q", got, "failed")
	}
	return segments[0].Count, segments[1].Count, segments[2].Count
}

// TestDoctor exercises `bosun doctor` end-to-end through the test
// harness. Each sub-test configures a project, forces a capability
// into a specific state via the fakes' error knobs, runs the command,
// and asserts on the rows and summary the user would see.
func TestDoctor(t *testing.T) {
	t.Run("all_configured/reports_each_provider_success", func(t *testing.T) {
		h := newDoctorHarness(t)

		runDoctor(t, h)

		// Every integration reports ✓ with the identity it resolved —
		// the provider name plus whatever the auth probe returned.
		assertPassed(t, h, rowTracker, "jira")
		assertPassed(t, h, rowCodeHost, "github → testuser")
		assertPassed(t, h, rowNotification, "slack → bosun-test")
		assertPassed(t, h, rowCICD, "github actions → testuser")
	})

	t.Run("all_configured/groups_checks_by_kind", func(t *testing.T) {
		// The three groups are the diagnostic's structure: local
		// environment, project configuration, external integrations.
		// A check landing in the wrong group misfiles the problem.
		h := newDoctorHarness(t)

		runDoctor(t, h)

		want := map[string]string{
			rowGlobalConfig:   "environment",
			rowProjectConfig:  "environment",
			rowGit:            "environment",
			rowRepositories:   "project",
			rowBranchTemplate: "project",
			rowStatusMappings: "project",
			rowTracker:        "integrations",
			rowCodeHost:       "integrations",
			rowNotification:   "integrations",
			rowCICD:           "integrations",
		}
		for name, group := range want {
			if got := doctorRow(t, h, name).Group; got != group {
				t.Errorf("%s: group = %q, want %q", name, got, group)
			}
		}
	})

	t.Run("all_configured/rows_share_one_value_column", func(t *testing.T) {
		// doctor pre-computes the alignment width across every check
		// in every group so the "name · value" dividers line up down
		// the whole report, not just within a group. That width is
		// invisible outside a rendered terminal, so assert on the
		// value the Reporter was handed.
		h := newDoctorHarness(t)

		runDoctor(t, h)

		// Widest check name is "branch template" / "status mappings".
		const wantAlign = len("status mappings")
		for name := range map[string]bool{
			rowGlobalConfig: true, rowGit: true, rowRepositories: true,
			rowStatusMappings: true, rowTracker: true, rowCICD: true,
		} {
			if got := doctorRow(t, h, name).Align; got != wantAlign {
				t.Errorf("%s: align width = %d, want %d — every row shares one column",
					name, got, wantAlign)
			}
		}
	})

	t.Run("all_configured/summary_counts_every_check", func(t *testing.T) {
		h := newDoctorHarness(t)

		runDoctor(t, h)

		passed, warned, failed := doctorSummary(t, h)
		if passed != 10 || warned != 0 || failed != 0 {
			t.Errorf("summary = %d passed, %d warned, %d failed; want 10/0/0\n%s",
				passed, warned, failed, h.Reporter.Dump())
		}
	})

	t.Run("all_configured/reports_the_configured_repositories", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.Workspace.AddRepo("web")

		runDoctor(t, h)

		row := doctorRow(t, h, rowRepositories)
		for _, name := range []string{"api", "web"} {
			if !strings.Contains(row.Value, name) {
				t.Errorf("repositories value = %q, want it to list %q", row.Value, name)
			}
		}
	})

	t.Run("tracker_unconfigured/reports_tracker_warning_only", func(t *testing.T) {
		// No issue_tracker.provider at all: the row reads "not
		// configured", which is a warning rather than a failure — an
		// absent integration isn't broken.
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(withoutConfig(t, "  provider: \"jira\"\n"))

		runDoctor(t, h)

		assertNotConfigured(t, h, rowTracker)
		assertHealthyExcept(t, h, rowTracker)
	})

	t.Run("tracker_unconfigured/unsupported_provider_names_it", func(t *testing.T) {
		// A provider bosun doesn't implement is a config mistake, not
		// an absent integration — the row has to name the offending
		// value so the user can fix the typo.
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(strings.Replace(doctorConfig,
			`provider: "jira"`, `provider: "linear"`, 1))

		runDoctor(t, h)

		assertWarned(t, h, rowTracker, `unsupported: "linear"`)
		assertHealthyExcept(t, h, rowTracker)
	})

	t.Run("tracker_unconfigured/incomplete_config_names_missing_keys", func(t *testing.T) {
		// Provider set but its required keys missing: doctor lists
		// exactly which ones, without attempting a connection.
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(`
repositories: ["repos/*"]
issue_tracker:
  provider: "jira"
`)

		runDoctor(t, h)

		assertWarned(t, h, rowTracker, "missing: base_url, email, token")
		if calls := h.Tracker.Calls(); len(calls) != 0 {
			t.Errorf("tracker calls = %v, want none — incomplete config should short-circuit", calls)
		}
	})

	t.Run("tracker_failing/construction_error_reads_as_configured_but_failing", func(t *testing.T) {
		// The tracker is configured; building it fails (bad token,
		// unreachable base URL). That must not read as "not set up".
		h := newDoctorHarness(t)
		h.Tracker.NewErr = errors.New("invalid api token")

		runDoctor(t, h)

		assertWarned(t, h, rowTracker, "configured but failing — invalid api token")
		assertHealthyExcept(t, h, rowTracker)
	})

	t.Run("tracker_failing/auth_rejection_is_reported_as_auth", func(t *testing.T) {
		// A 401/403 from the probe fetch is an auth problem, and the
		// row says so instead of leaking the raw transport error.
		h := newDoctorHarness(t)
		h.Tracker.GetErr = errors.New("jira: 401 Unauthorized")

		runDoctor(t, h)

		assertWarned(t, h, rowTracker, "auth failed (check token and email)")
	})

	t.Run("tracker_failing/probe_404_counts_as_healthy", func(t *testing.T) {
		// 404 means we authenticated and the probe issue simply
		// doesn't exist — the healthy outcome against a real tracker.
		// The row shows the endpoint identity it authenticated to.
		h := newDoctorHarness(t)
		h.Tracker.GetErr = errors.New("jira: 404 Not Found")

		runDoctor(t, h)

		assertPassed(t, h, rowTracker, "jira → acme.atlassian.net (dev@acme.test)")
		assertHealthyExcept(t, h)
	})

	t.Run("code_host_unconfigured/reports_host_warning_only", func(t *testing.T) {
		// The code host has no provider gate in doctor — it probes by
		// constructing. A constructor failure is how "no token
		// configured" surfaces, and it reads as not configured.
		//
		// CI/CD is dropped from the config here so the check
		// short-circuits before it reaches the code host; with it
		// configured, a broken host takes the CI/CD row down too (see
		// the cicd_shares_the_code_host_token scenario).
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(withoutConfig(t, "cicd:\n  provider: \"github_actions\"\n"))
		h.Host.NewErr = errors.New("no github token configured")

		runDoctor(t, h)

		assertNotConfigured(t, h, rowCodeHost)
		assertHealthyExcept(t, h, rowCodeHost, rowCICD)
		assertNotConfigured(t, h, rowCICD)
	})

	t.Run("code_host_unconfigured/cicd_shares_the_code_host_token", func(t *testing.T) {
		// TODO(arch #28): the GitHub Actions adapter verifies its auth
		// through the code host, so a broken host fails the CI/CD row
		// as well. Pinned deliberately — when cicd.CICD grows its own
		// AuthTest, this expectation changes with it.
		h := newDoctorHarness(t)
		h.Host.NewErr = errors.New("no github token configured")

		runDoctor(t, h)

		assertNotConfigured(t, h, rowCodeHost)
		assertWarned(t, h, rowCICD, "cannot verify token: no github token configured")
	})

	t.Run("code_host_failing/reports_auth_failure", func(t *testing.T) {
		// Host builds fine, the token is rejected: a different
		// problem from an absent host, and labeled as such.
		h := newDoctorHarness(t)
		h.Host.AuthErr = errors.New("401 Bad credentials")

		runDoctor(t, h)

		assertWarned(t, h, rowCodeHost, "auth failed: 401 Bad credentials")
	})

	t.Run("notifier_unconfigured/reports_notifier_warning_only", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(withoutConfig(t, "  provider: \"slack\"\n"))

		runDoctor(t, h)

		assertNotConfigured(t, h, rowNotification)
		assertHealthyExcept(t, h, rowNotification)
		if calls := h.Notifier.Calls(); len(calls) != 0 {
			t.Errorf("notifier calls = %v, want none — an unset provider shouldn't be probed", calls)
		}
	})

	t.Run("notifier_failing/construction_error_reads_as_configured_but_failing", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.Notifier.NewErr = errors.New("missing slack token")

		runDoctor(t, h)

		assertWarned(t, h, rowNotification, "configured but failing — missing slack token")
		assertHealthyExcept(t, h, rowNotification)
	})

	t.Run("notifier_failing/auth_rejection_is_reported_as_auth", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.Notifier.AuthTestErr = errors.New("invalid_auth")

		runDoctor(t, h)

		assertWarned(t, h, rowNotification, "auth failed: invalid_auth")
	})

	t.Run("notifier_failing/unreachable_channels_fail_the_row", func(t *testing.T) {
		// Auth succeeds but the channel probes don't: the row still
		// lists every channel (so the user sees which ones exist) and
		// warns overall.
		h := newDoctorHarness(t)
		h.Notifier.FindThreadErr = errors.New("channel_not_found")

		runDoctor(t, h)

		row := doctorRow(t, h, rowNotification)
		if row.Kind != ui.CaptureSkip {
			t.Errorf("notification: state = %q, want %q", row.Kind, ui.CaptureSkip)
		}
		for _, want := range []string{"slack → bosun-test", "#reviews", "#releases"} {
			if !strings.Contains(row.Value, want) {
				t.Errorf("notification value = %q, want it to contain %q", row.Value, want)
			}
		}
	})

	t.Run("notifier_configured/probes_every_configured_channel", func(t *testing.T) {
		// Both configured channels are probed, and they're the ones
		// from the config. Asserting on the arguments rather than the
		// call count matters: the fake answers any channel with a zero
		// ThreadRef and no error, so probing the wrong channel — or
		// the same one twice — reports success just as loudly as
		// probing correctly.
		h := newDoctorHarness(t)

		runDoctor(t, h)

		got := h.Notifier.FindThreadChannels()
		want := []string{"reviews", "releases"}
		if !slices.Equal(got, want) {
			t.Errorf("probed channels = %v, want %v (review then prerelease)", got, want)
		}
	})

	t.Run("cicd_unconfigured/reports_cicd_warning_only", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.Workspace.WriteConfig(withoutConfig(t, "cicd:\n  provider: \"github_actions\"\n"))

		runDoctor(t, h)

		assertNotConfigured(t, h, rowCICD)
		assertHealthyExcept(t, h, rowCICD)
	})

	t.Run("cicd_failing/reports_initialization_failure", func(t *testing.T) {
		h := newDoctorHarness(t)
		h.CICD.NewErr = errors.New("no workflows configured")

		runDoctor(t, h)

		assertWarned(t, h, rowCICD, "cannot initialize: no workflows configured")
		assertHealthyExcept(t, h, rowCICD)
	})

	t.Run("multiple_failures/reports_each_independently", func(t *testing.T) {
		// Three capabilities broken three different ways at once.
		// Each row carries its own diagnosis — no first-failure
		// short-circuit, no shared error text.
		h := newDoctorHarness(t)
		h.Tracker.NewErr = errors.New("invalid api token")
		h.Host.AuthErr = errors.New("401 Bad credentials")
		h.Notifier.AuthTestErr = errors.New("invalid_auth")

		runDoctor(t, h)

		assertWarned(t, h, rowTracker, "configured but failing — invalid api token")
		assertWarned(t, h, rowCodeHost, "auth failed: 401 Bad credentials")
		assertWarned(t, h, rowNotification, "auth failed: invalid_auth")
		// The CI/CD check borrows the code host's token, so it fails
		// with the host's error rather than one of its own.
		assertWarned(t, h, rowCICD, "auth failed: 401 Bad credentials")

		passed, warned, failed := doctorSummary(t, h)
		if passed != 6 || warned != 4 || failed != 0 {
			t.Errorf("summary = %d passed, %d warned, %d failed; want 6/4/0\n%s",
				passed, warned, failed, h.Reporter.Dump())
		}
	})

	t.Run("environment/missing_global_config_fails", func(t *testing.T) {
		// The required checks are the ones that produce ✗. Without a
		// global config the check fails hard and names the path it
		// looked at, so the user knows where to create it.
		h := testharness.New(t)
		h.Workspace.WriteConfig(doctorConfig)
		h.Workspace.AddRepo("api")
		h.InstallCICD()
		h.Tracker.SeedIssue(issue.Issue{Key: doctorProbeIssue, Title: "connectivity probe"})

		runDoctor(t, h)

		assertFailed(t, h, rowGlobalConfig, h.GlobalConfigPath())
		_, _, failed := doctorSummary(t, h)
		if failed != 1 {
			t.Errorf("failed count = %d, want 1\n%s", failed, h.Reporter.Dump())
		}
	})

	t.Run("environment/reports_the_resolved_config_paths", func(t *testing.T) {
		h := newDoctorHarness(t)

		runDoctor(t, h)

		assertPassed(t, h, rowGlobalConfig, h.GlobalConfigPath())
		assertPassed(t, h, rowProjectConfig, ".bosun/config.yaml")
		// git's value is the local git version, so assert its shape
		// rather than a literal: present, and stripped of the
		// "git version " prefix the command shells out for.
		git := doctorRow(t, h, rowGit)
		if git.Value == "" || strings.HasPrefix(git.Value, "git version") {
			t.Errorf("git value = %q, want a bare version string", git.Value)
		}
	})

	t.Run("no_project/exits_with_clear_error", func(t *testing.T) {
		// Pointed at a directory that isn't a bosun project, the run
		// fails before any check executes — with an error naming the
		// directory and what was missing, not a partial report.
		h := newDoctorHarness(t)
		notAProject := t.TempDir()

		err := h.Run("doctor", "--project", notAProject)

		if err == nil {
			t.Fatalf("doctor in a non-project succeeded, want an error\n%s", h.Reporter.Dump())
		}
		if !strings.Contains(err.Error(), ".bosun") || !strings.Contains(err.Error(), notAProject) {
			t.Errorf("error = %q, want it to name .bosun/ and %q", err, notAProject)
		}
		if events := h.Reporter.Events(); len(events) != 0 {
			t.Errorf("reported %d events before failing, want none\n%s",
				len(events), h.Reporter.Dump())
		}
	})

	// doctor is a report, not a gate: it exits 0 whatever it finds, so
	// that a broken integration doesn't take down a shell script that
	// runs it for information. Issue #24 assumed the opposite (a
	// non-zero exit when any check fails) — these scenarios pin the
	// behavior as built. Making doctor gate is a product decision, not
	// a test fix; see the PR description.
	t.Run("exit_code/zero_when_all_pass", func(t *testing.T) {
		h := newDoctorHarness(t)

		if err := h.Run("doctor"); err != nil {
			t.Fatalf("doctor: %v\n%s", err, h.Reporter.Dump())
		}
	})

	t.Run("exit_code/zero_when_checks_fail", func(t *testing.T) {
		// A required check failing (✗) and three integrations broken
		// still leaves the command successful.
		h := testharness.New(t)
		h.Workspace.WriteConfig(doctorConfig)
		h.InstallCICD()
		h.Tracker.NewErr = errors.New("invalid api token")
		h.Host.NewErr = errors.New("no github token configured")
		h.Notifier.NewErr = errors.New("missing slack token")

		if err := h.Run("doctor"); err != nil {
			t.Fatalf("doctor: %v\n%s", err, h.Reporter.Dump())
		}

		_, warned, failed := doctorSummary(t, h)
		if failed == 0 {
			t.Errorf("failed count = 0, want the global-config check to fail\n%s", h.Reporter.Dump())
		}
		if warned == 0 {
			t.Errorf("warned count = 0, want the broken integrations to warn\n%s", h.Reporter.Dump())
		}
	})
}
