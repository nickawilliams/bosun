package cli

import (
	"slices"
	"testing"

	"github.com/nickawilliams/bosun/internal/provider"
	"github.com/spf13/viper"
)

// TestScopeForKey covers the resolution rule the whole scope check
// rests on: an exact declaration governs its own path, and otherwise
// the longest declared ancestor governs, with a map-shaped group
// winning a tie.
//
// The `services` rows are the reason the tie rule exists. It is
// declared both ways at the same path — a map-shaped group for the
// central `services.<repo>` form and a plain key for the descriptor
// form — so the path itself and everything beneath it must answer
// differently. Getting that backwards reports every project's services
// block as misplaced.
func TestScopeForKey(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	keyScope, mapScope := schemaScopeSurface()

	tests := []struct {
		name, key string
		want      provider.Scope
		known     bool
	}{
		{"declared central key", "workspace.root", provider.ScopeCentral, true},
		{"unannotated key defaults central", "ui.color", provider.ScopeCentral, true},
		{"repo-scoped policy key", "pull_request.base", provider.ScopeAny, true},
		{"repo-scoped list key", "pull_request.reviewers", provider.ScopeAny, true},

		// services, both halves.
		{"services bare key is the descriptor form", "services", provider.ScopeRepo, true},
		{"services.<repo> is the central form", "services.api", provider.ScopeCentral, true},
		{"deep under the central services map", "services.api.billing", provider.ScopeCentral, true},

		// release.target, which is a KEY whose subtree is its own
		// business — so the subtree inherits the key's scope.
		{"release target", "cicd.workflows.release.target", provider.ScopeAny, true},
		{"release target per repo", "cicd.workflows.release.target.api", provider.ScopeAny, true},

		// A map-shaped group whose members are the user's to name.
		{"user-chosen status", "issue_tracker.statuses.triage", provider.ScopeCentral, true},

		{"not in the schema at all", "nonsense.key", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := scopeForKey(tt.key, keyScope, mapScope)
			if known != tt.known {
				t.Fatalf("scopeForKey(%q) known = %v, want %v", tt.key, known, tt.known)
			}
			if known && got != tt.want {
				t.Errorf("scopeForKey(%q) = %d, want %d", tt.key, got, tt.want)
			}
		})
	}
}

// TestMisplacedConfigKeys drives the walk over synthetic layers, which
// is what lets it cover the repo layer without a workspace on disk.
func TestMisplacedConfigKeys(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	layer := func(scope provider.Scope, label string, keys ...string) configLayer {
		return configLayer{scope: scope, label: label, keys: keys}
	}
	keysOf := func(issues []scopeIssue) []string {
		out := make([]string, len(issues))
		for i, iss := range issues {
			out[i] = iss.Key
		}
		return out
	}

	t.Run("central layers may set central keys", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeGlobal, "global config", "issue_tracker.token", "ui.color"),
			layer(provider.ScopeProject, "project config", "workspace.repositories", "services.api"),
		}, "")
		if len(got) != 0 {
			t.Errorf("misplaced = %v, want none", keysOf(got))
		}
	})

	t.Run("a descriptor may set repo-scoped policy", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml",
				"pull_request.base", "pull_request.reviewers", "services",
				"cicd.workflows.release.target"),
		}, "")
		if len(got) != 0 {
			t.Errorf("misplaced = %v, want none", keysOf(got))
		}
	})

	// The rule that makes the descriptor safe to commit. A token in a
	// shared repository is the failure this whole scope apparatus
	// exists to name, and it must be named without anyone having
	// annotated the token key.
	t.Run("a descriptor may not carry a credential", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml", "code_host.token"),
		}, "")
		if !slices.Equal(keysOf(got), []string{"code_host.token"}) {
			t.Fatalf("misplaced = %v, want the committed token reported", keysOf(got))
		}
		if got[0].Detail != "not allowed in api/.bosun.yaml" {
			t.Errorf("detail = %q, want it to name the offending file", got[0].Detail)
		}
	})

	t.Run("a descriptor may not redefine the workspace", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml", "workspace.repositories", "workspace.root"),
		}, "")
		if !slices.Equal(keysOf(got), []string{"workspace.repositories", "workspace.root"}) {
			t.Errorf("misplaced = %v, want both workspace keys reported", keysOf(got))
		}
	})

	// The wrong FORM of a right key: a descriptor already names its
	// repository, so the central repo-keyed spelling is inert there.
	// Reporting it is the difference between "your services config does
	// nothing" and silence.
	t.Run("a descriptor may not use the central repo-keyed form", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml", "services.api"),
		}, "")
		if !slices.Equal(keysOf(got), []string{"services.api"}) {
			t.Errorf("misplaced = %v, want services.api reported", keysOf(got))
		}
	})

	// And the mirror: the descriptor's bare form written centrally,
	// where it names no repository and so configures nothing.
	t.Run("a central layer may not use the bare descriptor form", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeProject, "project config", "services"),
		}, "")
		if !slices.Equal(keysOf(got), []string{"services"}) {
			t.Errorf("misplaced = %v, want the bare services key reported", keysOf(got))
		}
	})

	// Descriptors are never merged into the global viper, so this walk
	// is the ONLY thing that can see an unknown key in one.
	t.Run("unknown descriptor keys are reported here", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml", "pull_reqeust.base"),
		}, "")
		if !slices.Equal(keysOf(got), []string{"pull_reqeust.base"}) {
			t.Fatalf("misplaced = %v, want the typo reported", keysOf(got))
		}
		if got[0].Detail != "not in schema (api/.bosun.yaml)" {
			t.Errorf("detail = %q, want it to name the file", got[0].Detail)
		}
	})

	// Central unknown keys reach the user through unknownConfigKeys,
	// which walks the merged view. Reporting them here as well would
	// show the same key under two headings.
	t.Run("unknown central keys are left to the merged walk", func(t *testing.T) {
		got := misplacedConfigKeys([]configLayer{
			layer(provider.ScopeGlobal, "global config", "display.color"),
		}, "")
		if len(got) != 0 {
			t.Errorf("misplaced = %v, want none — unknownConfigKeys owns this", keysOf(got))
		}
	})

	t.Run("the group filter narrows the walk", func(t *testing.T) {
		layers := []configLayer{
			layer(provider.ScopeRepo, "api/.bosun.yaml", "code_host.token", "workspace.root"),
		}
		if got := misplacedConfigKeys(layers, "workspace"); !slices.Equal(keysOf(got), []string{"workspace.root"}) {
			t.Errorf("filtered = %v, want only workspace.root", keysOf(got))
		}
	})
}

// TestDescriptorCount pins the summary's note, which exists to
// distinguish "no descriptor problems" from "no descriptors read".
func TestDescriptorCount(t *testing.T) {
	layers := []configLayer{
		{scope: provider.ScopeGlobal, label: "global config"},
		{scope: provider.ScopeProject, label: "project config"},
		{scope: provider.ScopeRepo, label: "api/.bosun.yaml"},
		{scope: provider.ScopeRepo, label: "web/.bosun.yaml"},
	}
	if got := descriptorCount(layers); got != 2 {
		t.Errorf("descriptorCount() = %d, want 2", got)
	}
	if got := descriptorCount(layers[:2]); got != 0 {
		t.Errorf("descriptorCount() with no descriptors = %d, want 0", got)
	}
}

// TestConfigLayersWithoutRepositories pins the degradation the
// validation-reach decision rests on: `config check` walks descriptors
// when it can resolve the repositories and stays quiet when it can't,
// rather than failing a check of the central config over it.
func TestConfigLayersWithoutRepositories(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	// No workspace.repositories — resolveRepositories errors, and the
	// walk must still return the central layers it did reach.
	layers := configLayers(&configSources{})
	if got := descriptorCount(layers); got != 0 {
		t.Errorf("descriptorCount() = %d, want 0 when repositories can't be resolved", got)
	}
}
