package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/viper"
)

// writeDescriptor creates a repository directory carrying the given
// .bosun.yaml body and returns it as a Repository. The name is the
// directory basename, which is the key the central repo-keyed maps use.
func writeDescriptor(t *testing.T, name, body string) Repository {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, config.RepoConfigFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Repository{Name: name, Path: dir}
}

// TestRepoConfigFallsBackToCentral is the bootstrapping contract: a
// repository with no descriptor resolves every repo-scoped key from the
// central layers, exactly as it did before the layer existed. The
// central config is a permanent fallback, not a migration shim.
func TestRepoConfigFallsBackToCentral(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("pull_request.base", "develop")
	viper.Set("pull_request.reviewers", []string{"alice"})
	viper.Set("services.api", []string{"api", "worker"})

	rc := loadRepoConfig(writeDescriptor(t, "api", ""))

	if got := rc.String("pull_request.base"); got != "develop" {
		t.Errorf("base = %q, want the central value", got)
	}
	if got := rc.StringSlice("pull_request.reviewers"); !slices.Equal(got, []string{"alice"}) {
		t.Errorf("reviewers = %v, want the central list", got)
	}
	if got := rc.repoKeyed("services"); got == nil {
		t.Error("services = nil, want the central services.<repo> entry")
	}
}

// TestRepoConfigDescriptorWins covers the layering itself: most
// specific wins, for scalars and lists alike.
func TestRepoConfigDescriptorWins(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("pull_request.base", "develop")
	viper.Set("pull_request.reviewers", []string{"alice"})

	rc := loadRepoConfig(writeDescriptor(t, "api", `
pull_request:
  base: release/2.0
  reviewers: [bob]
`))

	if got := rc.String("pull_request.base"); got != "release/2.0" {
		t.Errorf("base = %q, want the descriptor's value", got)
	}

	// Replace, not merge. This is the decision the feature exists to
	// enable: appending would keep requesting alice on a repository
	// that explicitly named someone else, which is the workspace-wide
	// fan-out the per-repo layer is here to end.
	got := rc.StringSlice("pull_request.reviewers")
	if !slices.Equal(got, []string{"bob"}) {
		t.Errorf("reviewers = %v, want [bob] — the descriptor's list REPLACES the central one", got)
	}
}

// TestRepoConfigEmptyListOptsOut pins the escape hatch replacement
// semantics buy: a repository that wants none of the workspace's
// reviewers says so with an empty list. Without this, opting out would
// be impossible — there is no value that means "nothing" if absence
// means "inherit".
func TestRepoConfigEmptyListOptsOut(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("pull_request.reviewers", []string{"alice", "bob"})

	rc := loadRepoConfig(writeDescriptor(t, "api", "pull_request:\n  reviewers: []\n"))

	if got := rc.StringSlice("pull_request.reviewers"); len(got) != 0 {
		t.Errorf("reviewers = %v, want empty — an explicit [] opts the repo out", got)
	}
}

// TestRepoConfigRepoKeyedDropsTheRepoLevel covers the one place the two
// layers hold different shapes. Centrally, services is a map keyed by
// repository name; in a descriptor it is the bare value, because the
// file already names the repository.
//
// The second half is the security-shaped one: a descriptor that spells
// the CENTRAL form must not take effect, or a repository could
// configure its siblings by naming them.
func TestRepoConfigRepoKeyedDropsTheRepoLevel(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("services.api", "central-api")

	t.Run("descriptor form wins", func(t *testing.T) {
		rc := loadRepoConfig(writeDescriptor(t, "api", "services: [billing, search]\n"))
		got, ok := rc.repoKeyed("services").([]any)
		if !ok || len(got) != 2 {
			t.Fatalf("services = %#v, want the descriptor's two-entry list", rc.repoKeyed("services"))
		}
	})

	t.Run("a descriptor cannot configure another repo", func(t *testing.T) {
		// This descriptor belongs to "api" but names "web". repoKeyed
		// reads the bare key, so the nested form is simply not the key
		// it looks at — "api" still resolves centrally, and "web" is
		// untouched.
		rc := loadRepoConfig(writeDescriptor(t, "api", "services:\n  web: hijacked\n"))
		got := rc.repoKeyed("services")
		m, isMap := got.(map[string]any)
		if !isMap {
			t.Fatalf("services = %#v, want the descriptor's map", got)
		}
		if _, leaked := m["api"]; leaked {
			t.Error("the descriptor's repo-keyed form leaked into api's own resolution")
		}

		web := loadRepoConfig(Repository{Name: "web", Path: t.TempDir()})
		if v := web.repoKeyed("services"); v != nil {
			t.Errorf("web resolved %v from another repo's descriptor", v)
		}
	})
}

// TestRepoConfigMalformedDescriptorDegrades pins that one broken file
// does not take down a fan-out over the others: the repository falls
// back to central config rather than erroring the command.
func TestRepoConfigMalformedDescriptorDegrades(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("pull_request.base", "develop")

	rc := loadRepoConfig(writeDescriptor(t, "api", "pull_request: [broken\n"))

	if got := rc.String("pull_request.base"); got != "develop" {
		t.Errorf("base = %q, want the central fallback after a parse failure", got)
	}
}
