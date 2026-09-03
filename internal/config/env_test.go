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
func TestLoadEnvDoesNotShadowConfigBlocks(t *testing.T) {
	dir := t.TempDir()
	bosunDir := filepath.Join(dir, ".bosun")
	if err := os.Mkdir(bosunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("workspace:\n  root: _workspaces\n  repositories:\n    - ./*\n" +
		"issue_tracker:\n  project: EX\n")
	if err := os.WriteFile(filepath.Join(bosunDir, "config.yaml"), body, 0o644); err != nil {
		t.Fatal(err)
	}

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
	if got := viper.GetString("issue_tracker.project"); got != "EX" {
		t.Errorf("issue_tracker.project = %q, want EX", got)
	}
}
