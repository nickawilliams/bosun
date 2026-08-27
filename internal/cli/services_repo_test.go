package cli

import (
	"slices"
	"testing"

	"github.com/spf13/viper"
)

// TestResolveRepoServiceNamesLayers covers the service topology across
// both layers. Service topology is branch-scoped in a way a central map
// structurally cannot express — a branch that adds a service, or moves
// one between repositories, changes what "affected" means for that
// branch — so the descriptor has to win outright.
func TestResolveRepoServiceNamesLayers(t *testing.T) {
	tests := []struct {
		name       string
		central    any // services.api, or nil for unset
		descriptor string
		want       []string
	}{
		{
			name: "unconfigured falls back to the repo name",
			want: []string{"api"},
		},
		{
			name:    "central string",
			central: "central-api",
			want:    []string{"central-api"},
		},
		{
			// []any, not []string: that is what a YAML list decodes to,
			// and the resolver switches on the decoded shape. A test
			// that seeded []string would exercise a shape no config
			// file can produce.
			name:    "central list",
			central: []any{"a", "b"},
			want:    []string{"a", "b"},
		},
		{
			name:       "descriptor string wins",
			central:    "central-api",
			descriptor: "services: repo-api\n",
			want:       []string{"repo-api"},
		},
		{
			name:       "descriptor list wins",
			central:    "central-api",
			descriptor: "services: [billing, search]\n",
			want:       []string{"billing", "search"},
		},
		{
			// The branch-adds-a-service case: the descriptor names a
			// service the central map never heard of, and detection
			// picks it up on this branch alone.
			name:       "descriptor adds a service the centre never knew",
			central:    []any{"api"},
			descriptor: "services: [api, notifications]\n",
			want:       []string{"api", "notifications"},
		},
		{
			// An explicitly empty list is how a repository says it
			// contributes nothing deployable — repoHasServices reads
			// this as false and drops the repo from the surfaces.
			name:       "descriptor empty list means no services",
			central:    "central-api",
			descriptor: "services: []\n",
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			if tt.central != nil {
				viper.Set("services.api", tt.central)
			}

			r := writeDescriptor(t, "api", tt.descriptor)
			got := resolveRepoServiceNames(r)
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)

			if !slices.Equal(got, want) {
				t.Errorf("resolveRepoServiceNames() = %v, want %v", got, want)
			}
			if hasServices := repoHasServices(r); hasServices != (len(want) > 0) {
				t.Errorf("repoHasServices() = %v, want %v", hasServices, len(want) > 0)
			}
		})
	}
}

// TestResolveRepoServiceNamesMapForm covers the map shape, whose keys
// are service names and whose `_shared` member is a path bucket rather
// than a service.
func TestResolveRepoServiceNamesMapForm(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	r := writeDescriptor(t, "api", `
services:
  billing: [billing/]
  search: [search/]
  _shared: [go.mod]
`)

	got := resolveRepoServiceNames(r)
	slices.Sort(got)
	if !slices.Equal(got, []string{"billing", "search"}) {
		t.Errorf("services = %v, want the two real services (not _shared)", got)
	}
}

// TestResolveServicePathsReadsTheSameLayer is the pairing that keeps
// narrowing honest: the names and the path prefixes must come from ONE
// layer. A descriptor that redefines the services while the central map
// still supplied the paths would narrow the new services by the old
// service's prefixes — a silent wrong answer rather than a failure.
func TestResolveServicePathsReadsTheSameLayer(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("services.api", map[string]any{"legacy": []any{"legacy/"}})

	r := writeDescriptor(t, "api", "services:\n  billing: [billing/]\n")

	paths := resolveServicePaths(r)
	if _, stale := paths["legacy"]; stale {
		t.Errorf("paths = %v, want the central map's services gone", paths)
	}
	if !slices.Equal(paths["billing"], []string{"billing/"}) {
		t.Errorf("paths[billing] = %v, want the descriptor's prefixes", paths["billing"])
	}
}

// TestResolveServicePathsNonMapForms pins that only the map form yields
// a path filter; a string or list means "any change affects everything
// this repo builds".
func TestResolveServicePathsNonMapForms(t *testing.T) {
	for _, body := range []string{"services: api\n", "services: [a, b]\n", ""} {
		viper.Reset()
		if got := resolveServicePaths(writeDescriptor(t, "api", body)); got != nil {
			t.Errorf("resolveServicePaths(%q) = %v, want nil", body, got)
		}
		viper.Reset()
	}
}

// TestResolveServicePathsScalarValue covers the single-prefix spelling,
// where a service names one path rather than a list of them.
func TestResolveServicePathsScalarValue(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	paths := resolveServicePaths(writeDescriptor(t, "api", "services:\n  billing: billing/\n"))
	if !slices.Equal(paths["billing"], []string{"billing/"}) {
		t.Errorf("paths[billing] = %v, want the scalar lifted into a one-entry list", paths["billing"])
	}
}
