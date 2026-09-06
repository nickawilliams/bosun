package cli

import (
	"slices"
	"testing"

	"github.com/spf13/viper"
)

// TestSchemaEnvBindings locks the shape of the registration table —
// the single authority on what the environment can address. The rules
// under test: a scalar key binds at its own path with the explicit
// EnvVar ahead of the computed name; a map-shaped group binds at the
// group path only, its members deliberately absent; provider keys are
// bound as the union across every registered provider.
func TestSchemaEnvBindings(t *testing.T) {
	b := schemaEnvBindings()

	t.Run("explicit EnvVar precedes the computed name", func(t *testing.T) {
		got, ok := b["code_host.token"]
		if !ok {
			t.Fatal("code_host.token has no binding")
		}
		want := []string{"GITHUB_TOKEN", "BOSUN_CODE_HOST_TOKEN"}
		if !slices.Equal(got, want) {
			t.Errorf("code_host.token bindings = %v, want %v", got, want)
		}
	})

	t.Run("map-shaped group binds at the group path only", func(t *testing.T) {
		if _, ok := b["issue_tracker.statuses"]; !ok {
			t.Error("issue_tracker.statuses (the map key itself) has no binding")
		}
		if names, ok := b["issue_tracker.statuses.ready"]; ok {
			t.Errorf("statuses.ready is individually bound (%v); members of a map-shaped group must not be — env supplies the whole map at the group key", names)
		}
	})

	t.Run("provider keys are the union across providers", func(t *testing.T) {
		// slack's token is bound even though notification.provider is
		// unset here — which provider is configured can itself arrive
		// via env, so the table cannot depend on it.
		got, ok := b["notification.token"]
		if !ok {
			t.Fatal("notification.token (slack-contributed) has no binding")
		}
		if !slices.Contains(got, "BOSUN_SLACK_TOKEN") {
			t.Errorf("notification.token bindings = %v, want BOSUN_SLACK_TOKEN among them", got)
		}
	})

	t.Run("the splice marker never becomes a binding", func(t *testing.T) {
		for key := range b {
			if key == "issue_tracker."+providerKeysMarker {
				t.Errorf("marker key %q leaked into the bindings table", key)
			}
		}
	})
}

// TestAppendMissingDedupes pins the guard that keeps the bindings
// table free of repeated names: today's schema happens to produce no
// duplicates, so the branch is unreachable through schemaEnvBindings —
// but the moment two providers contribute the same key, or a key's
// EnvVar collides with its computed name, this is what keeps the
// registration from consulting the same variable twice.
func TestAppendMissingDedupes(t *testing.T) {
	got := appendMissing([]string{"GITHUB_TOKEN"}, "GITHUB_TOKEN")
	if len(got) != 1 {
		t.Errorf("appendMissing duplicated an existing name: %v", got)
	}
	got = appendMissing(got, "BOSUN_CODE_HOST_TOKEN")
	if len(got) != 2 {
		t.Errorf("appendMissing dropped a new name: %v", got)
	}
}

// TestResolveKeySourceAttributesBoundGroupEnv covers the attribution
// path a map-shaped group's own key takes: findConfigKey knows only
// leaf keys, so the group path falls through to resolveKeySource,
// which must still report the environment as the source when the
// group's binding is what supplies the value.
func TestResolveKeySourceAttributesBoundGroupEnv(t *testing.T) {
	t.Setenv("BOSUN_ISSUE_TRACKER_STATUSES", `{"ready":"Backlog"}`)

	value, source := (&configSources{}).resolveKeySource("issue_tracker.statuses")
	if source != sourceEnv {
		t.Errorf("source = %q, want %q", source, sourceEnv)
	}
	if value != `{"ready":"Backlog"}` {
		t.Errorf("value = %q, want the raw env payload", value)
	}
}

// TestBoundEnvValueIgnoresUnboundKeys locks the attribution side of
// the registry: a variable that matches a key nothing binds supplies
// no value, so `config show` cannot report influence the resolution
// doesn't have (the pre-#114 mismatch, in both directions).
func TestBoundEnvValueIgnoresUnboundKeys(t *testing.T) {
	t.Setenv("BOSUN_TOTALLY_UNKNOWN_KEY", "boo")
	if got := boundEnvValue("totally.unknown.key"); got != "" {
		t.Errorf("boundEnvValue for an unbound key = %q, want empty", got)
	}

	// Members of a map-shaped group are unbound by design.
	t.Setenv("BOSUN_ISSUE_TRACKER_STATUSES_READY", "Sneaky")
	if got := boundEnvValue("issue_tracker.statuses.ready"); got != "" {
		t.Errorf("boundEnvValue for a map member = %q, want empty", got)
	}
}

// TestMapGroupValues locks the accessor every consumer of a map-shaped
// key reads through: schema defaults underneath, the group's own viper
// value — file children, or an env JSON map at the group's binding —
// overlaid on top.
func TestMapGroupValues(t *testing.T) {
	t.Run("defaults alone", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()

		m := mapGroupValues("issue_tracker.statuses")
		if got := m["ready"]; got != "Ready" {
			t.Errorf(`ready = %q, want the schema default "Ready"`, got)
		}
	})

	t.Run("file children overlay defaults", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		_ = viper.MergeConfigMap(map[string]any{
			"issue_tracker": map[string]any{
				"statuses": map[string]any{"ready": "To Do", "triage": "Triage"},
			},
		})

		m := mapGroupValues("issue_tracker.statuses")
		if got := m["ready"]; got != "To Do" {
			t.Errorf("ready = %q, want the file value", got)
		}
		if got := m["triage"]; got != "Triage" {
			t.Errorf("triage = %q — a user-chosen member must come through", got)
		}
		if got := m["done"]; got != "Done" {
			t.Errorf("done = %q, want the untouched schema default", got)
		}
	})

	t.Run("env JSON overrides the block wholesale", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		_ = viper.MergeConfigMap(map[string]any{
			"issue_tracker": map[string]any{
				"statuses": map[string]any{"ready": "To Do"},
			},
		})
		t.Setenv("BOSUN_ISSUE_TRACKER_STATUSES", `{"ready":"Backlog"}`)
		bindSchemaEnv()

		m := mapGroupValues("issue_tracker.statuses")
		if got := m["ready"]; got != "Backlog" {
			t.Errorf("ready = %q, want the env value to beat the file", got)
		}
		// Env replaces the block, not the defaults beneath it.
		if got := m["done"]; got != "Done" {
			t.Errorf("done = %q, want the schema default backstop", got)
		}
	})
}

// TestResolveStatusHonorsEnvBinding is the read-path half of the
// map-shaped framing: an operator exporting the group's variable must
// see it in the status resolution the lifecycle commands use — the
// concatenated-child-path read this replaced silently ignored it.
func TestResolveStatusHonorsEnvBinding(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	t.Setenv("BOSUN_ISSUE_TRACKER_STATUSES", `{"in_progress":"Doing"}`)
	bindSchemaEnv()

	got, err := resolveStatus("in_progress")
	if err != nil {
		t.Fatalf("resolveStatus: %v", err)
	}
	if got != "Doing" {
		t.Errorf("in_progress = %q, want the env-supplied mapping", got)
	}

	// A key the env map doesn't name still resolves from its default.
	got, err = resolveStatus("done")
	if err != nil {
		t.Fatalf("resolveStatus(done): %v", err)
	}
	if got != "Done" {
		t.Errorf("done = %q, want the schema default", got)
	}
}

// TestBuildNotifyContentHonorsEnvBinding pins the union read: the
// templates group's env binding carries the whole map as JSON, and an
// entry may be a plain-text template (string) or structured override
// fields (map) — both shapes must come through the single group read.
func TestBuildNotifyContentHonorsEnvBinding(t *testing.T) {
	t.Run("string entry is a text template", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		t.Setenv("BOSUN_NOTIFICATION_TEMPLATES", `{"review":"issue {{.Issue.Key}}"}`)
		bindSchemaEnv()

		c := buildNotifyContent("review", notifyTemplateData{Issue: issueRef{Key: "EX-1"}})
		if c.Structured() {
			t.Fatal("Structured() = true, want a flat text render for a string entry")
		}
		if c.Text != "issue EX-1" {
			t.Errorf("Text = %q, want the env template rendered", c.Text)
		}
	})

	t.Run("map entry overrides structured fields", func(t *testing.T) {
		t.Cleanup(viper.Reset)
		viper.Reset()
		t.Setenv("BOSUN_NOTIFICATION_TEMPLATES", `{"review":{"header":"Custom Header"}}`)
		bindSchemaEnv()

		c := buildNotifyContent("review", notifyTemplateData{Issue: issueRef{Key: "EX-1"}})
		if c.Header != "Custom Header" {
			t.Errorf("Header = %q, want the env override", c.Header)
		}
		if c.Context != "via bosun" {
			t.Errorf("Context = %q, want the built-in default for an un-overridden field", c.Context)
		}
	})
}

// TestUnknownKeysAcceptBoundGroupPaths guards the unknown-key walk
// against the walk's own bindings: viper.AllKeys lists every bound key
// whether or not its variable is set, so the map-shaped group paths —
// now bound — must read as the schema's own keys, not as strangers.
func TestUnknownKeysAcceptBoundGroupPaths(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	bindSchemaEnv()

	if got := unknownConfigKeys(""); len(got) != 0 {
		t.Errorf("a freshly bound schema reported unknown keys: %v", got)
	}
}
