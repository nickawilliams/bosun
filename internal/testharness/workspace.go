// Package testharness provides end-to-end test fixtures for bosun
// commands. The harness wires up a temp project directory, fake
// capability providers, and stream-injected cobra commands so tests
// exercise the full RunE path without subprocesses or real network
// calls.
package testharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Owner is the GitHub owner every workspace repo is created under.
// AddRepo wires each repo's origin as git@github.com:acme/<name>.git so
// production code that parses the remote for a GitHub identity
// (gh.ParseRemote) resolves owner/name without network access.
const Owner = "acme"

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

	// onAddRepo, when set, is invoked for each repo AddRepo creates.
	// The harness uses it to link repos to the code-host fake.
	onAddRepo func(*Repo)
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

// ConfigPath is the absolute path to the project config file,
// whether or not it exists — the file `bosun init` writes.
func (w *Workspace) ConfigPath() string {
	return filepath.Join(w.Dir, ".bosun", "config.yaml")
}

// ReadConfig returns the raw contents of .bosun/config.yaml, failing
// the test if it can't be read. The round-trip counterpart to
// WriteConfig: use it to assert on what a command persisted.
func (w *Workspace) ReadConfig() string {
	w.t.Helper()
	b, err := os.ReadFile(w.ConfigPath())
	if err != nil {
		w.t.Fatalf("read config: %v", err)
	}
	return string(b)
}

// Initialized reports whether the workspace still has its .bosun/
// directory. NewWorkspace creates one, so this is true unless a test
// called Uninitialize.
func (w *Workspace) Initialized() bool {
	info, err := os.Stat(filepath.Join(w.Dir, ".bosun"))
	return err == nil && info.IsDir()
}

// Uninitialize removes .bosun/ so the workspace looks like a
// directory bosun has never seen. Needed by `bosun init`'s
// fresh-project scenarios: init branches on whether .bosun/ exists,
// and NewWorkspace creates it up front for every other command.
//
// Harness.Run stops injecting --project for an uninitialized
// workspace (the flag requires an existing .bosun/), so such a run
// resolves its project from the working directory — which the test
// must point at Dir via t.Chdir.
func (w *Workspace) Uninitialize() {
	w.t.Helper()
	if err := os.RemoveAll(filepath.Join(w.Dir, ".bosun")); err != nil {
		w.t.Fatalf("remove .bosun: %v", err)
	}
}

// AddRepo creates a git repository under repos/<name>/ with an
// initial commit on the default branch (main) and a bare remote at
// remotes/acme/<name>.git wired as origin with HEAD pointing at main.
// The remote matters because bosun's branch creation flow runs
// `git fetch origin main` and `git push -u origin <branch>`; without
// it, GetDefaultBranch fails on `git rev-parse origin/HEAD`.
//
// The origin URL is the GitHub-shaped git@github.com:acme/<name>.git —
// NOT the bare repo's path — so gh.ParseRemote resolves a repository
// identity. Real git transport still works because core.sshCommand
// points at a local shim that runs the pack command against remotes/
// instead of ssh-ing anywhere (see ensureGitShim).
//
// Returns a Repo handle for assertions on git state.
func (w *Workspace) AddRepo(name string) *Repo {
	w.t.Helper()

	// Bare repo serving as origin, laid out under the owner so the
	// ssh shim can resolve "acme/<name>.git" paths relative to remotes/.
	remote := filepath.Join(w.Dir, "remotes", Owner, name+".git")
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
	// A fresh repo inherits commit.gpgsign from the developer's global
	// config, so on a machine that signs by default every commit these
	// tests make — here and in the worktrees, which share this config —
	// invokes gpg. That turns a locked keyring or a hiccupping agent
	// into a test failure ("[GNUPG:] FAILURE sign") unrelated to
	// anything under test. Signed fixtures buy nothing; opt out.
	w.run(path, "git", "config", "commit.gpgsign", "false")

	readme := filepath.Join(path, "README.md")
	if err := os.WriteFile(readme, []byte("# "+name+"\n"), 0o644); err != nil {
		w.t.Fatalf("write README: %v", err)
	}
	w.run(path, "git", "add", "README.md")
	w.run(path, "git", "commit", "-m", "initial commit")

	// Wire origin + push + set HEAD so origin/HEAD → main is resolvable.
	// The URL is GitHub-shaped; the shim makes the transport local.
	url := fmt.Sprintf("git@github.com:%s/%s.git", Owner, name)
	w.run(path, "git", "remote", "add", "origin", url)
	w.run(path, "git", "config", "core.sshCommand", "'"+w.ensureGitShim()+"'")
	w.run(path, "git", "push", "-u", "origin", "main")
	w.run(path, "git", "remote", "set-head", "origin", "main")

	r := &Repo{t: w.t, Name: name, Owner: Owner, Path: path, RemotePath: remote}
	w.Repos = append(w.Repos, r)
	if w.onAddRepo != nil {
		w.onAddRepo(r)
	}
	return r
}

// ensureGitShim writes (once) and returns the GIT_SSH-style shim that
// makes git@github.com URLs resolve locally: git invokes it as
// `shim <host> "git-upload-pack 'acme/api.git'"`, and the shim drops
// the host and runs the pack command with remotes/ as the working
// directory, so 'acme/api.git' lands on the workspace's bare remote.
func (w *Workspace) ensureGitShim() string {
	w.t.Helper()
	path := filepath.Join(w.Dir, "gitshim")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	script := "#!/bin/sh\nshift\ncd \"$(dirname \"$0\")/remotes\" && eval \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		w.t.Fatalf("write git shim: %v", err)
	}
	return path
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
	// Owner is the GitHub owner in the repo's origin URL (see Owner).
	Owner string
	// Path is the absolute path to the repository root.
	Path string
	// RemotePath is the absolute path to the bare repository serving
	// as origin. Host-side effects (release tags cut by fakes.Host)
	// land here, mirroring GitHub creating tags server-side.
	RemotePath string
}

// HasBranch reports whether name exists as a local branch.
func (r *Repo) HasBranch(name string) bool {
	r.t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/"+name)
	cmd.Dir = r.Path
	return cmd.Run() == nil
}

// HasTag reports whether name exists as a tag on the repo's origin
// remote — where host-side release tags land. The local clone only
// sees such tags after a fetch, so assertions go straight to the
// remote rather than depending on fetch timing.
func (r *Repo) HasTag(name string) bool {
	r.t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/tags/"+name)
	cmd.Dir = r.RemotePath
	return cmd.Run() == nil
}

// Tag creates a lightweight tag at ref on the repo's origin remote —
// the seeding counterpart to HasTag. Use it to establish a "previous
// release" baseline the command's tag fetches and ancestry checks can
// see.
func (r *Repo) Tag(name, ref string) {
	r.t.Helper()
	cmd := exec.Command("git", "tag", name, ref)
	cmd.Dir = r.RemotePath
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git tag %s %s in %s: %v\n%s", name, ref, r.RemotePath, err, out)
	}
}

// RemoteRef resolves ref (e.g. "refs/heads/main", "refs/tags/v1.2.3")
// to a commit SHA on the repo's origin remote.
func (r *Repo) RemoteRef(ref string) string {
	r.t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", ref+"^{commit}")
	cmd.Dir = r.RemotePath
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("rev-parse %s in %s: %v", ref, r.RemotePath, err)
	}
	return strings.TrimSpace(string(out))
}

// Git runs a git command in dir, failing the test on error, and
// returns trimmed output. Exported for scenario setup that needs raw
// git operations beyond the Repo helpers (commits in worktrees,
// merges, pushes).
func Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
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
	for line := range strings.SplitSeq(string(out), "\n") {
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
