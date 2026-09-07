package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// TestBoundEnvReachesBareReads locks what replaced the materialization
// dance: bindSchemaEnv registers every schema key's env names with
// viper, so a bare viper.GetString — the read shape the provider
// factories and the whole command layer use — resolves the key's
// explicit EnvVar and the automatic BOSUN_* name directly. No
// intermediate ensureConfigValue/viper.Set step exists to skip
// (regression: config check honored env vars while requireConfig
// re-prompted for the same token, and 58 bare reads saw neither).
func TestBoundEnvReachesBareReads(t *testing.T) {
	t.Run("explicit EnvVar", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		t.Setenv("GITHUB_TOKEN", "sekrit")
		bindSchemaEnv()

		if got := viper.GetString("code_host.token"); got != "sekrit" {
			t.Errorf("code_host.token = %q, want the GITHUB_TOKEN value", got)
		}
	})

	t.Run("automatic BOSUN_* name", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		t.Setenv("BOSUN_ISSUE_TRACKER_BASE_URL", "https://x.example")
		bindSchemaEnv()

		if got := viper.GetString("issue_tracker.base_url"); got != "https://x.example" {
			t.Errorf("issue_tracker.base_url = %q, want the env value", got)
		}
	})

	t.Run("explicit EnvVar wins over the computed name", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		t.Setenv("GITHUB_TOKEN", "explicit")
		t.Setenv("BOSUN_CODE_HOST_TOKEN", "computed")
		bindSchemaEnv()

		if got := viper.GetString("code_host.token"); got != "explicit" {
			t.Errorf("code_host.token = %q, want the explicit EnvVar to win", got)
		}
	})

	t.Run("env beats the config file", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		// The file layer, loaded the way config.Load loads it —
		// viper.Set would land in the override layer, which outranks
		// env and would test the wrong rung.
		_ = viper.MergeConfigMap(map[string]any{
			"issue_tracker": map[string]any{"base_url": "https://file.example"},
		})
		t.Setenv("BOSUN_ISSUE_TRACKER_BASE_URL", "https://env.example")
		bindSchemaEnv()

		if got := viper.GetString("issue_tracker.base_url"); got != "https://env.example" {
			t.Errorf("issue_tracker.base_url = %q, want env to outrank the file", got)
		}
	})

	t.Run("schema default backstops the ladder", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		bindSchemaEnv()

		if got := viper.GetString("code_host.merge_method"); got != "squash" {
			t.Errorf("code_host.merge_method = %q, want the schema default", got)
		}
	})
}

// TestResolveGroupSkipsNoPromptKeys is the regression guard for a
// blocker the schema reshape opened. `issue_tracker.issue_pattern` is
// optional with no Default, and a provider adapter Requires its whole
// group (jira's New calls cfg.Require(issue.ConfigGroup)), so JIT
// resolution reached it on every command that builds a tracker — start,
// review, doctor, issue. Unset is the correct state for almost every
// user, so the prompt would have been permanent, and accepting the
// prefilled Example pins a Jira-shaped grammar over whichever tracker
// is actually configured.
//
// The session has to be interactive for the bug to exist at all, so
// this injects a reader — a non-*os.File input makes ui.Interactive
// true. The reader holds a single ctrl+c, so a prompt that should not
// happen surfaces as ErrCancelled rather than as a hang on a drained
// reader, which would burn the package timeout instead of failing here.
func TestResolveGroupSkipsNoPromptKeys(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	ui.SetStreams(strings.NewReader("\x03"), io.Discard, io.Discard)
	t.Cleanup(ui.ResetStreams)

	if !isInteractive() {
		t.Fatal("streams did not make the session interactive; the test would pass vacuously")
	}

	const groupName = "testgroup_noprompt"
	group := ConfigGroup{
		Label: "no-prompt probe",
		Keys: []ConfigKey{
			// Same shape as issue_pattern: optional, no Default, an
			// Example to prefill with — a prompt candidate but for the
			// flag.
			{Key: "escape_hatch", Label: "escape hatch", Example: "(EX-1)", NoPrompt: true},
		},
	}

	if err := resolveGroup(groupName, group); err != nil {
		t.Fatalf("resolveGroup prompted for a NoPrompt key: %v", err)
	}
	if got := viper.GetString(groupName + ".escape_hatch"); got != "" {
		t.Errorf("%s.escape_hatch = %q, want it left unset", groupName, got)
	}

	// forcePrompt too: the init wizard's reconfigure pass asks about
	// every key, set or not, and must still skip this one.
	if err := resolveGroupReconfigure(groupName, group); err != nil {
		t.Fatalf("reconfigure prompted for a NoPrompt key: %v", err)
	}
	if got := viper.GetString(groupName + ".escape_hatch"); got != "" {
		t.Errorf("%s.escape_hatch = %q after reconfigure, want it left unset", groupName, got)
	}
}

// TestNoPromptKeysAreDeclared pins that a NoPrompt key is still a
// schema key. Skipping it in resolution must not skip it in validation:
// `config check` has to accept it, `config show` has to render it, and
// the unknown-key walk has to know it exists.
func TestNoPromptKeysAreDeclared(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	ck, groupName, ok := findConfigKey(issue.ConfigGroup + ".issue_pattern")
	if !ok {
		t.Fatal("issue_tracker.issue_pattern is not in the schema")
	}
	if !ck.NoPrompt {
		t.Error("issue_tracker.issue_pattern lost NoPrompt — every tracker build would prompt for it")
	}
	if groupName != issue.ConfigGroup {
		t.Errorf("groupName = %q, want %q", groupName, issue.ConfigGroup)
	}

	viper.Set(issue.ConfigGroup+".issue_pattern", `(EX-\d+)`)
	if got := unknownConfigKeys(""); len(got) != 0 {
		t.Errorf("a set NoPrompt key was reported as unknown: %v", got)
	}
}

// TestResolveRepositoriesUnconfigured pins the error a project gets
// when nothing tells bosun where its repositories are. The message
// names the key, which is the only thing standing between the user and
// a guess — and the key moved in this branch, so a stale message would
// send them to `repositories:` at root, where it no longer has any
// effect.
func TestResolveRepositoriesUnconfigured(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	_, err := resolveRepositories(nil)
	if err == nil {
		t.Fatal("resolveRepositories succeeded with no patterns configured")
	}
	if !strings.Contains(err.Error(), "workspace.repositories") {
		t.Errorf("err = %v, want it to name workspace.repositories", err)
	}
}

// TestWithSubGroupKeysUnknownSubGroup pins that a sub-group name the
// schema doesn't have is skipped rather than fatal. serviceInitGroups
// names sub-groups as plain strings, so a typo or a renamed sub-group
// is a compile-clean mistake; degrading to "that sub-group contributes
// no fields" keeps `bosun init` usable while the wizard is one section
// short, which is the failure the user can actually see and report.
func TestWithSubGroupKeysUnknownSubGroup(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	group, ok := lookupGroup(notify.ConfigGroup)
	if !ok {
		t.Fatal("notification group missing from the schema")
	}
	before := len(group.Keys)

	ig := initGroup{
		Label:     "notifications",
		Group:     notify.ConfigGroup,
		SubGroups: []string{"nonexistent_subgroup"},
	}
	got := withSubGroupKeys(ig, group)
	if len(got.Keys) != before {
		t.Errorf("keys = %d, want the original %d — an unknown sub-group contributed fields",
			len(got.Keys), before)
	}

	// And the real one still does contribute, so the skip above isn't
	// passing by way of a walk that never resolves anything.
	ig.SubGroups = []string{"channels"}
	if got := withSubGroupKeys(ig, group); len(got.Keys) <= before {
		t.Errorf("keys = %d, want more than %d — channels contributed nothing",
			len(got.Keys), before)
	}
}
