package slack

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/provider"
)

// stubConfig is a provider.Config over a map, recording Require calls.
type stubConfig struct {
	values   map[string]string
	required []string
}

func (c *stubConfig) Get(key string) string { return c.values[key] }

func (c *stubConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	return nil
}

func TestDescriptorShape(t *testing.T) {
	d := Descriptor()

	if d.Name != "slack" {
		t.Errorf("Name = %q, want %q", d.Name, "slack")
	}

	byKey := map[string]provider.ConfigKey{}
	for _, ck := range d.Keys {
		byKey[ck.Key] = ck
	}

	// The two auth modes and the workspace the local mode needs are
	// Slack's own; the channel keys are bosun's, so they must NOT be
	// here (any chat provider routes to a named channel).
	for _, key := range []string{"auth", "token", "workspace"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("descriptor is missing the %q key", key)
		}
	}
	for _, key := range []string{"channel_review", "channel_prerelease", "provider"} {
		if _, ok := byKey[key]; ok {
			t.Errorf("descriptor claims %q, which belongs to bosun's own schema", key)
		}
	}

	if !byKey["token"].Secret {
		t.Error("token key is not marked Secret")
	}
	if got := byKey["token"].EnvVar; got != "BOSUN_SLACK_TOKEN" {
		t.Errorf("token EnvVar = %q, want BOSUN_SLACK_TOKEN", got)
	}
	if got := byKey["auth"].Options; len(got) != 2 || got[0] != "token" || got[1] != "local" {
		t.Errorf("auth Options = %v, want [token local]", got)
	}
}

func TestDescriptorNew(t *testing.T) {
	t.Run("token auth completes the group then builds", func(t *testing.T) {
		cfg := &stubConfig{values: map[string]string{
			notify.ConfigGroup + ".token": "xoxb-test",
		}}

		notifier, err := Descriptor().New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if notifier == nil {
			t.Fatal("New returned a nil notifier")
		}
		if len(cfg.required) != 1 || cfg.required[0] != notify.ConfigGroup {
			t.Errorf("Require calls = %v, want [%s]", cfg.required, notify.ConfigGroup)
		}
	})

	t.Run("local auth without a workspace is an error", func(t *testing.T) {
		// The local path resolves credentials out of the desktop app's
		// own storage, keyed by workspace name — without one there is
		// nothing to look up, and it must say so rather than prompt.
		cfg := &stubConfig{values: map[string]string{
			notify.ConfigGroup + ".auth": "local",
		}}

		_, err := Descriptor().New(cfg)
		if err == nil {
			t.Fatal("New succeeded with local auth and no workspace")
		}
		if !strings.Contains(err.Error(), "workspace required for local auth") {
			t.Errorf("error = %v, want it to name the missing workspace", err)
		}
		if len(cfg.required) != 0 {
			t.Errorf("Require calls = %v, want none — local auth doesn't prompt", cfg.required)
		}
	})
}
