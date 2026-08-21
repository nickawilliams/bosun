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
// one where an unset config key has to resolve to something.
func TestPreviewProvider(t *testing.T) {
	t.Run("unset falls back to the declared default", func(t *testing.T) {
		// The dispatch adapter shipped first and every config written
		// before the HTTP one omits the key. Falling back to "whichever
		// is the only one" — how the other capabilities behave — returns
		// "" here and would break all of them.
		p, err := PreviewProvider(cfg(nil), preview.Deps{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if p == nil {
			t.Fatal("PreviewProvider returned nil with no provider configured")
		}
		// It is the dispatch adapter: with no pipeline wired it reports
		// the pipeline missing, which the HTTP adapter has no notion of.
		if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); !errors.Is(err, preview.ErrNotConfigured) {
			t.Errorf("Create = %v, want a not-configured report from the cicd adapter", err)
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
	p, err := PreviewProvider(cfg(nil), deps)
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
// provider key's Default.
func TestPreviewDefaultProvider(t *testing.T) {
	if got := DefaultProvider(preview.ConfigGroup); got != "cicd" {
		t.Errorf("DefaultProvider(preview) = %q, want cicd", got)
	}
	// A capability with one provider and no declared default still has
	// an answer: the sole one.
	if got := DefaultProvider("issue_tracker"); got == "" {
		t.Error("DefaultProvider(issue_tracker) = \"\", want the sole tracker")
	}
	if got := DefaultProvider("not_a_group"); got != "" {
		t.Errorf("DefaultProvider(unknown) = %q, want empty", got)
	}
}

// TestDefaultNameWithoutADeclaredDefault covers the branch no shipped
// registry has: several providers, none flagged. There is nothing
// honest to prefill, so the key stays a real choice.
func TestDefaultNameWithoutADeclaredDefault(t *testing.T) {
	several := newRegistry("test capability", "test",
		entry[string]{name: "alpha"},
		entry[string]{name: "beta"},
	)
	if got := several.defaultName(); got != "" {
		t.Errorf("defaultName() = %q, want empty with no declared default", got)
	}
	if got := several.configured(cfg(nil)); got != "" {
		t.Errorf("configured() = %q, want empty with no declared default", got)
	}

	flagged := newRegistry("test capability", "test",
		entry[string]{name: "alpha"},
		entry[string]{name: "beta", dflt: true},
	)
	if got := flagged.defaultName(); got != "beta" {
		t.Errorf("defaultName() = %q, want beta", got)
	}
	// Config still wins over the default.
	if got := flagged.configured(cfg(map[string]string{"test.provider": "alpha"})); got != "alpha" {
		t.Errorf("configured() = %q, want alpha", got)
	}
}

// TestEveryPreviewProviderIsSelectable guards the registration a
// compiler can't: a descriptor with no name is unreachable through
// config, and two defaults means the fallback depends on list order.
func TestEveryPreviewProviderIsSelectable(t *testing.T) {
	var defaults int
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
		if d.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("%d preview descriptors are marked default, want exactly 1", defaults)
	}
}

// stubPipeline is a cicd.CICD that accepts anything, present only so the
// dispatch adapter gets past its pipeline check.
type stubPipeline struct{}

func (stubPipeline) TriggerWorkflow(context.Context, cicd.TriggerRequest) error { return nil }
func (stubPipeline) AuthTest(context.Context) (string, error)                   { return "", nil }
