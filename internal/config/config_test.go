package config

import (
	"os"
	"path/filepath"
	"strings"
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
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		_ = os.Chdir(nested)

		got := FindProjectRoot()
		if got != dir {
			t.Errorf("FindProjectRoot() = %q, want %q", got, dir)
		}
	})

	t.Run("override takes precedence", func(t *testing.T) {
		ProjectRootOverride = "/some/override/path"
		t.Cleanup(func() { ProjectRootOverride = "" })

		got := FindProjectRoot()
		if got != "/some/override/path" {
			t.Errorf("FindProjectRoot() = %q, want %q", got, "/some/override/path")
		}
	})

	t.Run("returns empty when no .bosun exists", func(t *testing.T) {
		dir := t.TempDir()

		origDir, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		_ = os.Chdir(dir)

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
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		_ = os.Chdir(dir)

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

		configContent := []byte("repositories:\n  - ./*\nworkspace:\n  root: _workspaces\n")
		if err := os.WriteFile(filepath.Join(bosunDir, "config.yaml"), configContent, 0o644); err != nil {
			t.Fatal(err)
		}

		origDir, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		_ = os.Chdir(dir)

		if err := Load(); err != nil {
			t.Errorf("Load() returned error: %v", err)
		}
	})
}

func TestLoadRepoConfig(t *testing.T) {
	// The ordinary case, and the one every repository starts in: no
	// descriptor at all. Absence must be a nil result and NOT an error,
	// because repo-scoped keys fall back to the central layers and a
	// repository without a descriptor has to keep working unchanged.
	t.Run("absent descriptor is not an error", func(t *testing.T) {
		v, err := LoadRepoConfig(t.TempDir())
		if err != nil {
			t.Fatalf("LoadRepoConfig() error = %v, want nil", err)
		}
		if v != nil {
			t.Errorf("LoadRepoConfig() = %v, want nil for a repo with no descriptor", v)
		}
	})

	t.Run("empty path is not an error", func(t *testing.T) {
		v, err := LoadRepoConfig("")
		if err != nil || v != nil {
			t.Errorf("LoadRepoConfig(\"\") = (%v, %v), want (nil, nil)", v, err)
		}
	})

	t.Run("reads the descriptor", func(t *testing.T) {
		dir := t.TempDir()
		body := []byte("services:\n  - billing\n  - search\npull_request:\n  base: develop\n")
		if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), body, 0o644); err != nil {
			t.Fatal(err)
		}

		v, err := LoadRepoConfig(dir)
		if err != nil {
			t.Fatalf("LoadRepoConfig() error = %v", err)
		}
		if v == nil {
			t.Fatal("LoadRepoConfig() = nil, want the parsed descriptor")
		}
		if got := v.GetString("pull_request.base"); got != "develop" {
			t.Errorf("pull_request.base = %q, want develop", got)
		}
		if got := v.GetStringSlice("services"); len(got) != 2 {
			t.Errorf("services = %v, want two entries", got)
		}
	})

	// A descriptor is committed to a repository bosun does not own, so
	// a broken one is a thing that happens to users rather than a
	// programming error. It must name the file: the caller fans out
	// over several repositories and "yaml: line 2" alone would not say
	// which one.
	t.Run("malformed descriptor errors and names the file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, RepoConfigFile), []byte("services: [oops\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadRepoConfig(dir)
		if err == nil {
			t.Fatal("LoadRepoConfig() succeeded on malformed YAML")
		}
		if !strings.Contains(err.Error(), RepoConfigFile) {
			t.Errorf("error %q does not name %s", err, RepoConfigFile)
		}
	})

	// The filename is load-bearing, not cosmetic: FindProjectRoot
	// returns the first ancestor holding a `.bosun/` DIRECTORY, so a
	// repository whose descriptor were a directory of that name would
	// become its own project root and shadow the workspace's config for
	// every command run inside it.
	t.Run("descriptor does not shadow the project root", func(t *testing.T) {
		root := t.TempDir()
		root, _ = filepath.EvalSymlinks(root)
		if err := os.Mkdir(filepath.Join(root, ".bosun"), 0o755); err != nil {
			t.Fatal(err)
		}

		repo := filepath.Join(root, "api")
		if err := os.Mkdir(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, RepoConfigFile), []byte("services: api\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		origDir, _ := os.Getwd()
		t.Cleanup(func() { _ = os.Chdir(origDir) })
		_ = os.Chdir(repo)

		if got := FindProjectRoot(); got != root {
			t.Errorf("FindProjectRoot() = %q, want the workspace root %q", got, root)
		}
	})
}
