package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/preview"
)

// TestPreviewProvider covers the selection this capability exists to
// make: preview is the one capability with two providers, so it is the
// one where an unset config key has no answer to fall back on.
func TestPreviewProvider(t *testing.T) {
	t.Run("unset is refused rather than guessed", func(t *testing.T) {
		// Two adapters and no declared default: an unset key is a choice
		// the user has not made. Building one anyway would report every
		// later failure against a provider they never named.
		_, err := PreviewProvider(cfg(nil), preview.Deps{})
		if err == nil {
			t.Fatal("PreviewProvider succeeded with no provider configured")
		}
		// The refusal has to name the options, since it is the whole
		// instruction the user gets.
		for _, want := range []string{"cicd", "ephemeral"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want it to offer %q", err, want)
			}
		}
	})

	t.Run("cicd is selectable by name", func(t *testing.T) {
		c := cfg(map[string]string{preview.ConfigGroup + ".provider": "cicd"})
		p, err := PreviewProvider(c, preview.Deps{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		// It is the dispatch adapter: with no pipeline wired it reports
		// the pipeline missing, which the HTTP adapter has no notion of.
		if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); !errors.Is(err, preview.ErrNotConfigured) {
			t.Errorf("Create = %v, want a not-configured report from the cicd adapter", err)
		}
		// And it cannot enumerate a fleet — the property that tells the
		// two adapters apart, asserted from both sides.
		if _, ok := p.(preview.Lister); ok {
			t.Error("configured cicd but got a provider that can list")
		}
	})

	t.Run("named provider is honored", func(t *testing.T) {
		c := cfg(map[string]string{
			preview.ConfigGroup + ".provider":     "ephemeral",
			preview.ConfigGroup + ".api.base_url": "https://ephemeral.example.dev",
		})
		p, err := PreviewProvider(c, preview.Deps{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		// Only the HTTP adapter can enumerate the fleet, so the Lister
		// assertion is what distinguishes the two.
		if _, ok := p.(preview.Lister); !ok {
			t.Error("configured ephemeral but got a provider that cannot list")
		}
	})

	t.Run("unknown provider is reported", func(t *testing.T) {
		c := cfg(map[string]string{preview.ConfigGroup + ".provider": "carrier-pigeon"})
		_, err := PreviewProvider(c, preview.Deps{})
		if err == nil {
			t.Fatal("PreviewProvider succeeded for an unregistered provider")
		}
		if !strings.Contains(err.Error(), "carrier-pigeon") {
			t.Errorf("error = %v, want it to name the unsupported value", err)
		}
	})

	t.Run("provider config errors propagate", func(t *testing.T) {
		// The HTTP adapter's base URL has no default; without it there
		// is nothing to talk to, and the failure has to surface rather
		// than yielding a provider that errors on every call.
		c := cfg(map[string]string{preview.ConfigGroup + ".provider": "ephemeral"})
		c.requireErr = errors.New("base_url not configured")
		if _, err := PreviewProvider(c, preview.Deps{}); err == nil {
			t.Fatal("PreviewProvider succeeded with no base URL")
		}
	})
}

// TestPreviewDepsReachTheAdapter pins the argument half of the seam:
// deps are passed per call, not held on the package-level registry the
// catalog uses, so a lost hand-off is invisible until a deploy
// dispatches nowhere.
func TestPreviewDepsReachTheAdapter(t *testing.T) {
	var asked bool
	// A pipeline is wired because Create checks for one before it
	// resolves targets; without it the call short-circuits and the
	// resolver never runs.
	deps := preview.Deps{
		Workflow: preview.WorkflowDeps{
			Stage:    "preview",
			Pipeline: stubPipeline{},
			Targets: func(context.Context, string) ([]preview.Target, error) {
				asked = true
				return nil, errors.New("resolver reached")
			},
		},
	}
	c := cfg(map[string]string{preview.ConfigGroup + ".provider": "cicd"})
	p, err := PreviewProvider(c, deps)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); err == nil {
		t.Fatal("Create succeeded despite a failing target resolver")
	}
	if !asked {
		t.Error("the target resolver was never called; Deps did not reach the adapter")
	}
}

// TestPreviewDefaultProvider pins what the config layer renders as the
// provider key's Default: nothing, because preview has two providers
// and picking between them is the user's job.
func TestPreviewDefaultProvider(t *testing.T) {
	if got := DefaultProvider(preview.ConfigGroup); got != "" {
		t.Errorf("DefaultProvider(preview) = %q, want empty with two providers", got)
	}
	// A capability with one provider still has an answer: the sole one.
	if got := DefaultProvider("issue_tracker"); got == "" {
		t.Error("DefaultProvider(issue_tracker) = \"\", want the sole tracker")
	}
	if got := DefaultProvider("not_a_group"); got != "" {
		t.Errorf("DefaultProvider(unknown) = %q, want empty", got)
	}
}

// TestConfiguredFallsBackOnlyToASoleProvider pins the one fallback the
// registry has left. Built from a local registry rather than the
// package's so both sizes are covered regardless of what ships.
func TestConfiguredFallsBackOnlyToASoleProvider(t *testing.T) {
	several := newRegistry("test capability", "test",
		entry[string]{name: "alpha"},
		entry[string]{name: "beta"},
	)
	if got := several.configured(cfg(nil)); got != "" {
		t.Errorf("configured() = %q, want empty with several registered", got)
	}
	// Config still resolves when there is no sole provider to fall back
	// on — the fallback is the only part that goes silent.
	if got := several.configured(cfg(map[string]string{"test.provider": "alpha"})); got != "alpha" {
		t.Errorf("configured() = %q, want alpha", got)
	}

	one := newRegistry("test capability", "test", entry[string]{name: "alpha"})
	if got := one.configured(cfg(nil)); got != "alpha" {
		t.Errorf("configured() = %q, want the sole provider", got)
	}
}

// TestEveryPreviewProviderIsSelectable guards the registration a
// compiler can't: a descriptor with no name is unreachable through
// config.
func TestEveryPreviewProviderIsSelectable(t *testing.T) {
	for _, d := range previewDescriptors {
		if d.Name == "" {
			t.Error("a preview descriptor has no name; nothing can select it")
		}
		if d.New == nil {
			t.Errorf("preview provider %q has no constructor", d.Name)
		}
		if !HasProvider(preview.ConfigGroup, d.Name) {
			t.Errorf("preview provider %q is not registered in the catalog", d.Name)
		}
	}
}

// stubPipeline is a cicd.CICD that accepts anything, present only so the
// dispatch adapter gets past its pipeline check.
type stubPipeline struct{}

func (stubPipeline) TriggerWorkflow(context.Context, cicd.TriggerRequest) error { return nil }
func (stubPipeline) AuthTest(context.Context) (string, error)                   { return "", nil }
