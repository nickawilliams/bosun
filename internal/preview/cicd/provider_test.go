package cicd

import (
	"context"
	"errors"
	"testing"
	"text/template"

	"github.com/nickawilliams/bosun/internal/preview"
)

func TestDescriptor(t *testing.T) {
	d := Descriptor()

	if d.Name != "cicd" {
		t.Errorf("Name = %q, want cicd", d.Name)
	}
	// Every config written before preview had a second provider omits
	// preview.provider, and those configs must keep selecting this one.
	if !d.Default {
		t.Error("Default = false; the adapter that shipped first must be the fallback")
	}
	// Its config lives under the CI/CD group, where it has always been.
	// Declaring the same keys here would prompt for them twice and
	// invite a second, divergent copy.
	if len(d.Keys) != 0 {
		t.Errorf("Keys = %v, want none", d.Keys)
	}
}

// TestDescriptorNewWiresDeps pins the CLI-to-adapter seam: the deps the
// CLI resolved have to reach the adapter, or a deploy dispatches to
// nothing and a binding is never written.
func TestDescriptorNewWiresDeps(t *testing.T) {
	pipeline := newFakePipeline()
	tracker := newFakeTracker()
	tmpl := template.Must(template.New("u").Parse("https://{{.Preview.Name}}.example.dev"))

	var askedFor []string
	p, err := Descriptor().New(nil, preview.Deps{
		Tracker:     tracker,
		URLTemplate: tmpl,
		Workflow: preview.WorkflowDeps{
			Pipeline: pipeline,
			Stage:    "preview",
			Targets: func(_ context.Context, subStage string) ([]preview.Target, error) {
				askedFor = append(askedFor, subStage)
				return []preview.Target{{Owner: "org", Repo: "tooling", Workflow: "up.yml", Label: "tooling"}}, nil
			},
			InputName: func(_, concept string) string { return concept },
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	env, err := p.Create(context.Background(), preview.Claim{IssueKey: "PROJ-1", Name: "brave-falcon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(askedFor) != 1 || askedFor[0] != "preview.up" {
		t.Errorf("target resolver asked for %v, want [preview.up]", askedFor)
	}
	if len(pipeline.triggers) != 1 {
		t.Fatalf("dispatched %d workflows, want 1", len(pipeline.triggers))
	}
	if got := pipeline.triggers[0].Workflow; got != "up.yml" {
		t.Errorf("dispatched %q, want up.yml — the Target didn't survive the mapping", got)
	}
	if env.URL != "https://brave-falcon.example.dev" {
		t.Errorf("URL = %q, want the templated URL — URLTemplate didn't reach the adapter", env.URL)
	}
	if tracker.setCalls != 1 {
		t.Errorf("SetProperty called %d times, want 1 — Tracker didn't reach the adapter", tracker.setCalls)
	}
}

// TestDescriptorNewTolerantOfPartialDeps pins the degraded shapes. A
// display path builds a provider with nothing wired; the result must
// report "not configured" rather than panic on a nil func.
func TestDescriptorNewTolerantOfPartialDeps(t *testing.T) {
	t.Run("no targets resolver", func(t *testing.T) {
		p, err := Descriptor().New(nil, preview.Deps{
			Workflow: preview.WorkflowDeps{Pipeline: newFakePipeline(), Stage: "preview"},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		// No resolver yields no targets, which is the same answer a
		// configured-but-empty stage gives.
		if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); !errors.Is(err, ErrNoWorkflow) {
			t.Errorf("Create = %v, want ErrNoWorkflow", err)
		}
		if err := p.Destroy(context.Background(), "PROJ-1", "brave-falcon"); !errors.Is(err, ErrNoWorkflow) {
			t.Errorf("Destroy = %v, want ErrNoWorkflow", err)
		}
	})

	t.Run("no input names", func(t *testing.T) {
		// Destroy dereferences InputName directly to find the name input.
		// A nil one must land on the documented refusal, not a panic.
		p, err := Descriptor().New(nil, preview.Deps{
			Workflow: preview.WorkflowDeps{
				Pipeline: newFakePipeline(),
				Stage:    "preview",
				Targets: func(context.Context, string) ([]preview.Target, error) {
					return []preview.Target{{Owner: "org", Repo: "tooling", Workflow: "down.yml"}}, nil
				},
			},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		err = p.Destroy(context.Background(), "PROJ-1", "brave-falcon")
		if err == nil {
			t.Fatal("Destroy succeeded with no name input configured")
		}
		if errors.Is(err, ErrNoWorkflow) {
			t.Errorf("Destroy = %v, want the no-name-input refusal", err)
		}
	})

	t.Run("no pipeline", func(t *testing.T) {
		p, err := Descriptor().New(nil, preview.Deps{})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); !errors.Is(err, ErrNoPipeline) {
			t.Errorf("Create = %v, want ErrNoPipeline", err)
		}
	})
}

// TestTargetResolverErrorsPropagate pins that a workflow-config error
// reaches the caller instead of reading as an empty target set, which
// would silently skip the deploy.
func TestTargetResolverErrorsPropagate(t *testing.T) {
	sentinel := errors.New("bad workflow path")
	p, err := Descriptor().New(nil, preview.Deps{
		Workflow: preview.WorkflowDeps{
			Pipeline: newFakePipeline(),
			Stage:    "preview",
			Targets: func(context.Context, string) ([]preview.Target, error) {
				return nil, sentinel
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, err := p.Create(context.Background(), preview.Claim{Name: "brave-falcon"}); !errors.Is(err, sentinel) {
		t.Errorf("Create = %v, want the resolver's error", err)
	}
}

// TestNotConfiguredSentinelsAreRecognizableThroughTheInterface pins the
// graceful-degradation path: a caller holding only preview.Provider
// must be able to tell "this provider has no backend" without importing
// the adapter to name its sentinel.
func TestNotConfiguredSentinelsAreRecognizableThroughTheInterface(t *testing.T) {
	for name, err := range map[string]error{"ErrNoPipeline": ErrNoPipeline, "ErrNoWorkflow": ErrNoWorkflow} {
		if !errors.Is(err, preview.ErrNotConfigured) {
			t.Errorf("%s does not match preview.ErrNotConfigured", name)
		}
	}
	// And they keep their own wording. These strings are what the
	// preview and cleanup commands print, and a %w wrap would prefix
	// each with "preview: provider not configured", turning a one-line
	// skip notice into a doubled sentence.
	if got, want := ErrNoPipeline.Error(), "preview: no CI/CD pipeline configured"; got != want {
		t.Errorf("ErrNoPipeline.Error() = %q, want %q", got, want)
	}
	if got, want := ErrNoWorkflow.Error(), "preview: no workflow configured for stage"; got != want {
		t.Errorf("ErrNoWorkflow.Error() = %q, want %q", got, want)
	}
	// Distinct sentinels, not one aliased to the other.
	if errors.Is(ErrNoPipeline, ErrNoWorkflow) {
		t.Error("ErrNoPipeline matches ErrNoWorkflow; the two are not distinguishable")
	}
	if errors.Is(ErrNoPipeline, preview.ErrNoEnvironment) {
		t.Error("ErrNoPipeline matches ErrNoEnvironment")
	}
}
