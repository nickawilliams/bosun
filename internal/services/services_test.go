package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
)

// stubConfig is a provider.Config over a map. requireErr stands in for
// the CLI's just-in-time completion failing (non-interactive session,
// user declined).
type stubConfig struct {
	values     map[string]string
	requireErr error
	required   []string
}

func cfg(kv map[string]string) *stubConfig { return &stubConfig{values: kv} }

func (c *stubConfig) Get(key string) string { return c.values[key] }

func (c *stubConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	return c.requireErr
}

// TestProviderMetadata covers the registry surface the config layer
// reads. It asserts on shape rather than exact provider lists so adding
// a provider doesn't fail it — except for the group→capability wiring,
// which is exactly what a new capability must get right.
func TestProviderMetadata(t *testing.T) {
	groups := []string{
		issue.ConfigGroup,
		code.ConfigGroup,
		notify.ConfigGroup,
		cicd.ConfigGroup,
	}

	for _, group := range groups {
		t.Run(group, func(t *testing.T) {
			names := ProviderNames(group)
			if len(names) == 0 {
				t.Fatalf("ProviderNames(%q) is empty — the group has no registry", group)
			}
			for _, name := range names {
				if !HasProvider(group, name) {
					t.Errorf("HasProvider(%q, %q) = false for a listed provider", group, name)
				}
			}
			if HasProvider(group, "definitely-not-a-provider") {
				t.Errorf("HasProvider(%q, …) accepted an unknown name", group)
			}
			if len(names) == 1 && SoleProvider(group) != names[0] {
				t.Errorf("SoleProvider(%q) = %q, want %q",
					group, SoleProvider(group), names[0])
			}
			if len(names) > 1 && SoleProvider(group) != "" {
				t.Errorf("SoleProvider(%q) = %q, want empty with several registered",
					group, SoleProvider(group))
			}
		})
	}

	t.Run("unknown group", func(t *testing.T) {
		if got := ProviderNames("workspace"); got != nil {
			t.Errorf("ProviderNames(workspace) = %v, want nil — it has no providers", got)
		}
		if got := ProviderKeys("workspace", "jira"); got != nil {
			t.Errorf("ProviderKeys(workspace, jira) = %v, want nil", got)
		}
		if HasProvider("workspace", "jira") {
			t.Error("HasProvider(workspace, jira) = true")
		}
		if got := SoleProvider("workspace"); got != "" {
			t.Errorf("SoleProvider(workspace) = %q, want empty", got)
		}
	})

	t.Run("keys are per provider", func(t *testing.T) {
		if got := ProviderKeys(issue.ConfigGroup, "jira"); len(got) == 0 {
			t.Error("ProviderKeys(issue_tracker, jira) is empty")
		}
		if got := ProviderKeys(issue.ConfigGroup, "nope"); got != nil {
			t.Errorf("ProviderKeys for an unknown provider = %v, want nil", got)
		}
	})

	t.Run("keys are copies", func(t *testing.T) {
		// Callers splice these into the config schema and set fields on
		// them (Source functions); handing out the registry's own slice
		// would let that scribble on every later read.
		first := ProviderKeys(issue.ConfigGroup, "jira")
		first[0].Label = "clobbered"
		if got := ProviderKeys(issue.ConfigGroup, "jira"); got[0].Label == "clobbered" {
			t.Error("ProviderKeys returned the registry's own slice")
		}
	})
}

func TestParseIssueIdentifier(t *testing.T) {
	t.Run("configured provider parses", func(t *testing.T) {
		c := cfg(map[string]string{issue.ConfigGroup + ".provider": "jira"})
		if got := ParseIssueIdentifier(c, "feature/EX-42_thing"); got != "EX-42" {
			t.Errorf("= %q, want EX-42", got)
		}
	})

	t.Run("unset provider falls back to the sole registered one", func(t *testing.T) {
		// The fallback is what keeps an unconfigured project's breadcrumb
		// working. With more than one tracker registered there is nothing
		// to fall back to and this yields "" instead.
		c := cfg(nil)
		want := "EX-42"
		if SoleProvider(issue.ConfigGroup) == "" {
			want = ""
		}
		if got := ParseIssueIdentifier(c, "feature/EX-42_thing"); got != want {
			t.Errorf("= %q, want %q", got, want)
		}
	})

	t.Run("unknown provider parses nothing", func(t *testing.T) {
		c := cfg(map[string]string{issue.ConfigGroup + ".provider": "linear"})
		if got := ParseIssueIdentifier(c, "feature/EX-42_thing"); got != "" {
			t.Errorf("= %q, want empty for an unregistered provider", got)
		}
	})

	t.Run("no key in the string", func(t *testing.T) {
		c := cfg(map[string]string{issue.ConfigGroup + ".provider": "jira"})
		if got := ParseIssueIdentifier(c, "main"); got != "" {
			t.Errorf("= %q, want empty", got)
		}
	})

	// Never constructs a tracker, so it never asks for credentials — the
	// whole reason this doesn't go through the Tracker interface.
	t.Run("never requires config", func(t *testing.T) {
		c := cfg(map[string]string{issue.ConfigGroup + ".provider": "jira"})
		ParseIssueIdentifier(c, "feature/EX-42_thing")
		if len(c.required) != 0 {
			t.Errorf("Require calls = %v, want none", c.required)
		}
	})
}

// TestIssueTrackerResolvesProviderFirst pins the tracker's construction
// policy: the provider pick is completed before anything else, because
// which keys the rest of the group even has depends on the answer.
func TestIssueTracker(t *testing.T) {
	t.Run("resolves the provider pick first", func(t *testing.T) {
		c := cfg(map[string]string{
			issue.ConfigGroup + ".provider": "jira",
			issue.ConfigGroup + ".base_url": "https://acme.atlassian.net",
			issue.ConfigGroup + ".email":    "dev@acme.test",
			issue.ConfigGroup + ".token":    "secret",
		})

		if _, err := IssueTracker(c); err != nil {
			t.Fatalf("IssueTracker: %v", err)
		}
		if len(c.required) == 0 || c.required[0] != issue.ConfigGroup+".provider" {
			t.Errorf("Require calls = %v, want the provider key first", c.required)
		}
	})

	t.Run("an unresolvable provider pick is the error", func(t *testing.T) {
		want := errors.New("provider not configured")
		c := cfg(nil)
		c.requireErr = want

		if _, err := IssueTracker(c); !errors.Is(err, want) {
			t.Errorf("error = %v, want %v", err, want)
		}
	})

	t.Run("an unregistered provider is unsupported", func(t *testing.T) {
		c := cfg(map[string]string{issue.ConfigGroup + ".provider": "linear"})

		_, err := IssueTracker(c)
		if err == nil {
			t.Fatal("IssueTracker succeeded for an unregistered provider")
		}
		if !strings.Contains(err.Error(), `unsupported issue tracker: "linear"`) {
			t.Errorf("error = %v, want it to name the capability and the value", err)
		}
	})
}

// TestCodeHostFallsBackToSoleProvider pins the host's laxer policy: it
// never prompts for a provider, because hosts discover credentials on
// their own and every repository command needs one.
func TestCodeHost(t *testing.T) {
	t.Run("unset provider uses the sole registered host", func(t *testing.T) {
		c := cfg(map[string]string{code.ConfigGroup + ".token": "gh-token"})

		host, err := CodeHost(c)
		if err != nil {
			t.Fatalf("CodeHost: %v", err)
		}
		if host == nil {
			t.Fatal("CodeHost returned nil")
		}
		if len(c.required) != 0 {
			t.Errorf("Require calls = %v, want none — a token was configured", c.required)
		}
	})

	t.Run("an unregistered provider is unsupported", func(t *testing.T) {
		c := cfg(map[string]string{
			code.ConfigGroup + ".provider": "gitlab",
			code.ConfigGroup + ".token":    "token",
		})

		_, err := CodeHost(c)
		if err == nil {
			t.Fatal("CodeHost succeeded for an unregistered provider")
		}
		if !strings.Contains(err.Error(), `unsupported code host: "gitlab"`) {
			t.Errorf("error = %v, want it to name the capability and the value", err)
		}
	})
}

func TestCICD(t *testing.T) {
	t.Run("unset provider uses the sole registered pipeline", func(t *testing.T) {
		c := cfg(map[string]string{code.ConfigGroup + ".token": "gh-token"})

		pipeline, err := CICD(c)
		if err != nil {
			t.Fatalf("CICD: %v", err)
		}
		if pipeline == nil {
			t.Fatal("CICD returned nil")
		}
	})

	t.Run("an unregistered provider is unsupported", func(t *testing.T) {
		c := cfg(map[string]string{cicd.ConfigGroup + ".provider": "circleci"})

		_, err := CICD(c)
		if err == nil {
			t.Fatal("CICD succeeded for an unregistered provider")
		}
		if !strings.Contains(err.Error(), `unsupported CI/CD provider: "circleci"`) {
			t.Errorf("error = %v, want it to name the capability and the value", err)
		}
	})
}

// TestNotifierIsOptIn pins the notifier's strictest policy: an unset
// provider is an error callers read as "skip notifications", never a
// prompt. Prompting here would ask every user of every lifecycle command
// to configure Slack.
func TestNotifier(t *testing.T) {
	t.Run("unset provider is an error, not a fallback", func(t *testing.T) {
		c := cfg(nil)

		_, err := Notifier(c)
		if err == nil {
			t.Fatal("Notifier succeeded with no provider configured")
		}
		if !strings.Contains(err.Error(), "notification provider not configured") {
			t.Errorf("error = %v, want the not-configured message", err)
		}
		if len(c.required) != 0 {
			t.Errorf("Require calls = %v, want none", c.required)
		}
	})

	t.Run("configured provider builds", func(t *testing.T) {
		c := cfg(map[string]string{
			notify.ConfigGroup + ".provider": "slack",
			notify.ConfigGroup + ".token":    "xoxb-test",
		})

		notifier, err := Notifier(c)
		if err != nil {
			t.Fatalf("Notifier: %v", err)
		}
		if notifier == nil {
			t.Fatal("Notifier returned nil")
		}
	})

	t.Run("an unregistered provider is unsupported", func(t *testing.T) {
		c := cfg(map[string]string{notify.ConfigGroup + ".provider": "teams"})

		_, err := Notifier(c)
		if err == nil {
			t.Fatal("Notifier succeeded for an unregistered provider")
		}
		if !strings.Contains(err.Error(), `unsupported notification provider: "teams"`) {
			t.Errorf("error = %v, want it to name the capability and the value", err)
		}
	})
}

// TestCatalogsCoverEveryRegistry guards the one thing the type system
// can't: catalogs is hand-maintained alongside the registries, and a
// capability missing from it silently answers "no providers" to the
// config layer — a group whose provider key renders with no options.
func TestCatalogsCoverEveryRegistry(t *testing.T) {
	want := map[string]int{
		issue.ConfigGroup:  len(trackerDescriptors),
		code.ConfigGroup:   len(hostDescriptors),
		notify.ConfigGroup: len(notifierDescriptors),
		cicd.ConfigGroup:   len(pipelineDescriptors),
	}

	if len(catalogs) != len(want) {
		t.Errorf("catalogs has %d groups, want %d", len(catalogs), len(want))
	}
	for group, n := range want {
		if got := len(ProviderNames(group)); got != n {
			t.Errorf("ProviderNames(%q) has %d entries, want %d", group, got, n)
		}
	}
}
