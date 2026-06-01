// Package testharness provides end-to-end test fixtures for bosun
// commands. The harness wires up a temp project directory, fake
// capability providers, and stream-injected cobra commands so tests
// exercise the full RunE path without subprocesses or real network
// calls.
package testharness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Workspace is a temp project directory with .bosun/config.yaml and
// any repositories the test seeds. Created via NewWorkspace; cleaned
// up automatically by t.TempDir.
type Workspace struct {
	t *testing.T

	// Dir is the absolute path to the workspace root (acts as the
	// project root — .bosun/ lives directly under it).
	Dir string

	// Repos collects repos added via AddRepo, in insertion order.
	Repos []*Repo
}

// NewWorkspace creates a fresh project directory with an empty .bosun/
// subdirectory. Call WriteConfig to populate config.yaml.
func NewWorkspace(t *testing.T) *Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".bosun"), 0o755); err != nil {
		t.Fatalf("create .bosun: %v", err)
	}
	return &Workspace{t: t, Dir: dir}
}

// WriteConfig overwrites .bosun/config.yaml with the given YAML.
// Callers can use this multiple times; only the last value applies.
func (w *Workspace) WriteConfig(yaml string) {
	w.t.Helper()
	path := filepath.Join(w.Dir, ".bosun", "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		w.t.Fatalf("write config: %v", err)
	}
}

// AddRepo creates a git repository under repos/<name>/ with an
// initial commit on the default branch (main) and a bare remote at
// remotes/<name>.git wired as origin with HEAD pointing at main.
// The remote matters because bosun's branch creation flow runs
// `git fetch origin main` and `git push -u origin <branch>`; without
// it, GetDefaultBranch fails on `git rev-parse origin/HEAD`.
//
// Returns a Repo handle for assertions on git state.
func (w *Workspace) AddRepo(name string) *Repo {
	w.t.Helper()

	// Bare repo serving as origin.
	remote := filepath.Join(w.Dir, "remotes", name+".git")
	if err := os.MkdirAll(remote, 0o755); err != nil {
		w.t.Fatalf("create remote dir: %v", err)
	}
	w.run(remote, "git", "init", "--bare", "-b", "main")

	// Working repo.
	path := filepath.Join(w.Dir, "repos", name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		w.t.Fatalf("create repo dir: %v", err)
	}
	w.run(path, "git", "init", "-b", "main")
	w.run(path, "git", "config", "user.email", "test@bosun.local")
	w.run(path, "git", "config", "user.name", "bosun test")

	readme := filepath.Join(path, "README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
		w.t.Fatalf("write README: %v", err)
	}
	w.run(path, "git", "add", "README.md")
	w.run(path, "git", "commit", "-m", "initial commit")

	// Wire origin + push + set HEAD so origin/HEAD → main is resolvable.
	w.run(path, "git", "remote", "add", "origin", remote)
	w.run(path, "git", "push", "-u", "origin", "main")
	w.run(path, "git", "remote", "set-head", "origin", "main")

	r := &Repo{t: w.t, Name: name, Path: path}
	w.Repos = append(w.Repos, r)
	return r
}

// run executes a command in dir, failing the test on non-zero exit.
func (w *Workspace) run(dir, name string, args ...string) {
	w.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		w.t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
}

// Repo represents a git repository created via Workspace.AddRepo.
type Repo struct {
	t *testing.T

	// Name is the directory basename (e.g., "api").
	Name string
	// Path is the absolute path to the repository root.
	Path string
}

// HasBranch reports whether name exists as a local branch.
func (r *Repo) HasBranch(name string) bool {
	r.t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+name)
	cmd.Dir = r.Path
	return cmd.Run() == nil
}

// WorktreeExists reports whether path is registered as a worktree of
// this repository. Compares paths through filepath.EvalSymlinks because
// macOS t.TempDir paths resolve through /private and `git worktree list`
// prints the resolved form.
func (r *Repo) WorktreeExists(path string) bool {
	r.t.Helper()
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = r.Path
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Fall back to absolute path if the worktree doesn't exist yet
		// on disk; comparison just won't match.
		want, err = filepath.Abs(path)
		if err != nil {
			return false
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		got := strings.TrimPrefix(line, "worktree ")
		if got == want {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(got); err == nil && resolved == want {
			return true
		}
	}
	return false
}
