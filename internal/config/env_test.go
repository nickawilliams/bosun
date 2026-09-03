package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

// The context env vars (BOSUN_ISSUE, BOSUN_PROJECT, BOSUN_WORKSPACE)
// share their names with top-level config blocks. Under viper's
// AutomaticEnv an env var matching a key path's first segment shadows
// everything beneath it, so `BOSUN_WORKSPACE=EX-1` made
// `workspace.root` resolve to empty and every command reading it fail
// with "workspaces not configured". Load must leave the config blocks
// readable no matter what is exported.
//
// The fixture carries `issue:` and `project:` blocks that the schema
// does not define, deliberately. Only `workspace` collides with a real
// block today, so a fixture of real blocks alone would pin one
// variable against one block and say nothing about the other two —
// which is the half of the fix that claims to retire the whole class.
// Load merges files without consulting the schema, so a synthetic
// block is the honest way to ask "would a block of this name survive?"
// before one exists to be broken.
func TestLoadEnvDoesNotShadowConfigBlocks(t *testing.T) {
	dir := t.TempDir()
	bosunDir := filepath.Join(dir, ".bosun")
	if err := os.Mkdir(bosunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("workspace:\n  root: _workspaces\n  repositories:\n    - ./*\n" +
		"issue:\n  pattern: EX-\\d+\n" +
		"project:\n  label: example\n")
	if err := os.WriteFile(filepath.Join(bosunDir, "config.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	// The global config is read too, so a developer's real
	// ~/.config/bosun/config.yaml would otherwise take part in this
	// run — a malformed one would fail it. Point the lookup at an
	// empty directory, as the CLI harness does.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	origDir, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	_ = os.Chdir(dir)

	t.Setenv("BOSUN_WORKSPACE", "EX-1-feature")
	t.Setenv("BOSUN_ISSUE", "EX-1")
	t.Setenv("BOSUN_PROJECT", "example")

	viper.Reset()
	t.Cleanup(viper.Reset)
	if err := Load(); err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if got := viper.GetString("workspace.root"); got != "_workspaces" {
		t.Errorf("workspace.root = %q, want _workspaces", got)
	}
	if got := viper.GetStringSlice("workspace.repositories"); len(got) != 1 || got[0] != "./*" {
		t.Errorf("workspace.repositories = %v, want [./*]", got)
	}
	if got := viper.GetString("issue.pattern"); got != `EX-\d+` {
		t.Errorf(`issue.pattern = %q, want EX-\d+`, got)
	}
	if got := viper.GetString("project.label"); got != "example" {
		t.Errorf("project.label = %q, want example", got)
	}

	// The second symptom, and the one a user sees rather than hits: a
	// shadowed block vanishes from AllSettings entirely, so `bosun
	// config show` reported a config file that did not mention its own
	// workspace root.
	settings := viper.AllSettings()
	for _, block := range []string{"workspace", "issue", "project"} {
		if _, ok := settings[block]; !ok {
			t.Errorf("AllSettings() is missing the %q block: %v", block, settings)
		}
	}
}
