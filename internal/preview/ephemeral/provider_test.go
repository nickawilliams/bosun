package ephemeral

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/provider"
)

// mapConfig is a provider.Config over a map. Require reports the first
// missing key rather than prompting — the non-interactive behavior the
// real implementation falls back to.
type mapConfig struct {
	values   map[string]string
	required []string
}

func (c *mapConfig) Get(key string) string { return c.values[key] }

func (c *mapConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	for _, k := range keys {
		if c.values[k] == "" {
			return errNotConfigured
		}
	}
	return nil
}

var errNotConfigured = errors.New("not configured")

func TestDescriptor(t *testing.T) {
	d := Descriptor()

	if d.Name != "ephemeral" {
		t.Errorf("Name = %q, want ephemeral", d.Name)
	}
	// The cicd adapter is the default; this one is opt-in, so every
	// config written before it existed keeps selecting cicd.
	if d.Default {
		t.Error("Default = true; the HTTP adapter must be opt-in")
	}

	var sawBaseURL bool
	for _, ck := range d.Keys {
		// Keys are relative to the group — the config layer prefixes
		// them. An absolute key here would resolve to
		// "preview.preview.api.base_url".
		if strings.HasPrefix(ck.Key, preview.ConfigGroup+".") {
			t.Errorf("key %q is absolute; descriptor keys are group-relative", ck.Key)
		}
		if ck.Key == keyBaseURL {
			sawBaseURL = true
			if !ck.Required {
				t.Error("api.base_url is not Required, but there is no default to fall back to")
			}
		}
	}
	if !sawBaseURL {
		t.Errorf("Keys %v omit %s", d.Keys, keyBaseURL)
	}
}

func TestDescriptorNew(t *testing.T) {
	t.Run("builds from config", func(t *testing.T) {
		cfg := &mapConfig{values: map[string]string{
			preview.ConfigGroup + "." + keyBaseURL: "https://ephemeral.example.dev",
		}}

		p, err := Descriptor().New(cfg, preview.Deps{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p == nil {
			t.Fatal("New returned a nil provider")
		}
		// The base URL is the one value with no discoverable default, so
		// it is the one the adapter asks for.
		if len(cfg.required) != 1 || cfg.required[0] != preview.ConfigGroup+"."+keyBaseURL {
			t.Errorf("Require called with %v, want just the base URL", cfg.required)
		}
	})

	t.Run("missing base URL", func(t *testing.T) {
		cfg := &mapConfig{values: map[string]string{}}

		if _, err := Descriptor().New(cfg, preview.Deps{}); !errors.Is(err, errNotConfigured) {
			t.Fatalf("err = %v, want the config layer's error", err)
		}
	})
}

// TestDescriptorPassesDepsThrough pins the seam: the tracker and URL
// template are resolved by the CLI, and a descriptor that dropped them
// would leave the adapter unable to read its own bindings.
func TestDescriptorPassesDepsThrough(t *testing.T) {
	cfg := &mapConfig{values: map[string]string{
		preview.ConfigGroup + "." + keyBaseURL: "https://ephemeral.example.dev",
	}}
	store := bound("brave-falcon")

	p, err := Descriptor().New(cfg, preview.Deps{Tracker: nil})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// Deps.Tracker is typed as issue.Tracker; a nil one must not become
	// a non-nil PropertyStore holding nil, which would panic on first
	// use rather than reading as "no registry".
	if _, err := p.Get(t.Context(), "PROJ-1"); !errors.Is(err, preview.ErrNoEnvironment) {
		t.Fatalf("err = %v, want ErrNoEnvironment for a nil tracker", err)
	}

	// And with a store wired in, the same call finds the binding.
	withStore := New(Options{BaseURL: "http://127.0.0.1:1", Token: staticToken, Tracker: store})
	if _, err := withStore.Get(t.Context(), "PROJ-1"); errors.Is(err, preview.ErrNoEnvironment) {
		t.Error("Get reported no environment despite a bound name")
	}
}

func staticToken(context.Context) (string, error) { return "t", nil }

var _ provider.Config = (*mapConfig)(nil)
