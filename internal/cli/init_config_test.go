package cli

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWriteInitConfig covers the generated .bosun/config.yaml template
// in both of its arms. The no-workspace-root arm is unreachable from
// the e2e scenarios — the prompt resolves an empty answer to its
// default, and the harness is always interactive — so it is exercised
// directly here.
//
// The property worth pinning is that the output PARSES, and parses to
// what it looks like. Repositories and the workspace root are both
// workspace config now, so the template emits one `workspace:` block
// with both beneath it; emitting a second `workspace:` key further down
// would be valid-looking YAML that silently drops one of the two.
func TestWriteInitConfig(t *testing.T) {
	parse := func(t *testing.T, path string) map[string]any {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var cfg map[string]any
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			t.Fatalf("generated config is not valid YAML: %v\n%s", err, raw)
		}
		return cfg
	}

	workspaceBlock := func(t *testing.T, cfg map[string]any) map[string]any {
		t.Helper()
		ws, ok := cfg["workspace"].(map[string]any)
		if !ok {
			t.Fatalf("workspace = %#v, want a map", cfg["workspace"])
		}
		return ws
	}

	t.Run("with a workspace root", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := writeInitConfig(path, "trees", []string{"repos/*"}); err != nil {
			t.Fatalf("writeInitConfig: %v", err)
		}

		ws := workspaceBlock(t, parse(t, path))
		if got := ws["root"]; got != "trees" {
			t.Errorf("workspace.root = %v, want trees", got)
		}
		repos, ok := ws["repositories"].([]any)
		if !ok || len(repos) != 1 || repos[0] != "repos/*" {
			t.Errorf("workspace.repositories = %#v, want [repos/*]", ws["repositories"])
		}
	})

	t.Run("without a workspace root", func(t *testing.T) {
		// Both halves commented out: no root was resolved and no globs
		// were detected. The block still has to parse, and `root` must
		// be absent rather than empty — an empty root would read as
		// "workspaces at the project root" instead of "not configured".
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := writeInitConfig(path, "", nil); err != nil {
			t.Fatalf("writeInitConfig: %v", err)
		}

		cfg := parse(t, path)
		ws := workspaceBlock(t, cfg)
		if _, ok := ws["root"]; ok {
			t.Errorf("workspace.root present with no root configured: %#v", ws)
		}
		if got := ws["repositories"]; got != nil {
			t.Errorf("workspace.repositories = %#v, want the empty placeholder", got)
		}
		// The commented-out integration examples must stay comments.
		if _, ok := cfg["issue_tracker"]; ok {
			t.Errorf("the commented-out template materialized real keys: %#v", cfg)
		}
	})
}
