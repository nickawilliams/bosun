package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindProjectRoot(t *testing.T) {
	t.Run("finds .bosun directory", func(t *testing.T) {
		dir := t.TempDir()
		// Resolve symlinks (macOS /var -> /private/var).
		dir, _ = filepath.EvalSymlinks(dir)

		bosunDir := filepath.Join(dir, ".bosun")
		if err := os.Mkdir(bosunDir, 0o755); err != nil {
			t.Fatal(err)
		}

		// Create a nested subdirectory and work from there.
		nested := filepath.Join(dir, "a", "b", "c")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		origDir, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(origDir) })
		os.Chdir(nested)

		got := FindProjectRoot()
		if got != dir {
			t.Errorf("FindProjectRoot() = %q, want %q", got, dir)
		}
	})

	t.Run("returns empty when no .bosun exists", func(t *testing.T) {
		dir := t.TempDir()

		origDir, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(origDir) })
		os.Chdir(dir)

		got := FindProjectRoot()
		if got != "" {
			t.Errorf("FindProjectRoot() = %q, want empty string", got)
		}
	})
}

func TestLoad(t *testing.T) {
	t.Run("succeeds with no config files", func(t *testing.T) {
		dir := t.TempDir()

		origDir, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(origDir) })
		os.Chdir(dir)

		if err := Load(); err != nil {
			t.Errorf("Load() returned error: %v", err)
		}
	})

	t.Run("loads project config", func(t *testing.T) {
		dir := t.TempDir()
		bosunDir := filepath.Join(dir, ".bosun")
		if err := os.Mkdir(bosunDir, 0o755); err != nil {
			t.Fatal(err)
		}

		configContent := []byte("repos:\n  - ./*\nworkspace_root: _workspaces\n")
		if err := os.WriteFile(filepath.Join(bosunDir, "config.yaml"), configContent, 0o644); err != nil {
			t.Fatal(err)
		}

		origDir, _ := os.Getwd()
		t.Cleanup(func() { os.Chdir(origDir) })
		os.Chdir(dir)

		if err := Load(); err != nil {
			t.Errorf("Load() returned error: %v", err)
		}
	})
}
