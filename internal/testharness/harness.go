package testharness

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/testharness/fakes"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// Harness is the entry point for end-to-end command tests. It owns a
// Workspace, a set of capability fakes, and the I/O streams driving
// the cobra command. Build via New; the cleanup is automatic.
type Harness struct {
	t *testing.T

	// Workspace is the temp project directory backing the test.
	Workspace *Workspace

	// Tracker is the in-memory issue tracker injected via SetServices.
	// Tests seed it with issues and assert on Calls() / Issues().
	Tracker *fakes.Tracker

	// Host is the in-memory code host injected via SetServices. Tests
	// seed PRs/releases and assert on Releases() / Calls(). Repos
	// added after Host exists are linked automatically by LinkRepo so
	// release tags land on the workspace's bare remotes.
	Host *fakes.Host

	// Notifier is the in-memory notifier injected via SetServices.
	// Tests assert on Messages() for what was posted.
	Notifier *fakes.Notifier

	stdin  *chunkReader
	stdout *syncBuffer
	stderr *syncBuffer
}

// syncBuffer is a mutex-guarded bytes.Buffer. Run wires the same
// buffer as a cobra command stream (written from the test goroutine)
// AND as the captureFD pipe sink (written from the copy goroutine) —
// a bare bytes.Buffer would race whenever a command writes through
// both os.Stdout and cmd.OutOrStdout.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

// New constructs a Harness with a fresh workspace, a fake tracker, and
// the global state (viper, ui streams, services factory, project root
// override) reset and restored on test cleanup.
//
// The harness mutates package-level state (cli.services, the ui
// stream vars, viper, config.ProjectRootOverride, the XDG_CONFIG_HOME
// env) via t.Cleanup-managed swaps. Tests using this harness must NOT
// call t.Parallel() — sibling tests would race on the shared state and
// produce nondeterministic failures. Sequential is fine; the per-test
// setup is fast (single-digit hundreds of milliseconds).
//
// Tracker, Host, and Notifier fakes install automatically. The
// remaining capability factories (CICD, PreviewProvider) default to
// functions that fail the test if invoked. Tests for commands needing
// those services install fakes via SetServices directly after calling
// New.
func New(t *testing.T) *Harness {
	t.Helper()
	h := &Harness{
		t:         t,
		Workspace: NewWorkspace(t),
		Tracker:   fakes.NewTracker(),
		Host:      fakes.NewHost(),
		Notifier:  fakes.NewNotifier(),
		stdin:     &chunkReader{},
		stdout:    &syncBuffer{},
		stderr:    &syncBuffer{},
	}

	// Link every repo to the code-host fake as it's added, so
	// host-side tag creation (CreateRelease) lands on the repo's bare
	// remote and GetLatestTag reads real tags back.
	h.Workspace.onAddRepo = func(r *Repo) {
		h.Host.LinkRepo(r.Owner, r.Name, r.RemotePath)
	}

	// Isolate viper, ui streams, and the project-root override so
	// state doesn't leak between tests in the same package.
	viper.Reset()
	t.Cleanup(viper.Reset)

	prevOverride := config.ProjectRootOverride
	t.Cleanup(func() { config.ProjectRootOverride = prevOverride })

	t.Cleanup(ui.ResetStreams)

	// Reset cli.Bootstrap's once-per-process guard so each test's
	// cmd.Execute re-runs config load + UI-mode setup against its
	// own fresh workspace and viper.
	cli.ResetBootstrap()
	t.Cleanup(cli.ResetBootstrap)

	prevServices := cli.GetServices()
	t.Cleanup(func() { cli.SetServices(prevServices) })

	// Point XDG_CONFIG_HOME at a scratch dir so config.Load() doesn't
	// read the developer's real global config during tests.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	cli.SetServices(&cli.Services{
		IssueTracker:    func() (issue.Tracker, error) { return h.Tracker, nil },
		CodeHost:        func() (code.Host, error) { return h.Host, nil },
		CICD:            notInstalled[cicd.CICD](t, "CICD"),
		Notifier:        func() (notify.Notifier, error) { return h.Notifier, nil },
		PreviewProvider: notInstalledPreview(t),
	})

	return h
}

// Type appends s to the input the cobra command reads from. Use this
// to pre-fill answers for interactive prompts (e.g., "y" for
// confirmations, "branch-slug\r" for input fields).
//
// Each Type call is delivered as one chunk, with a short pause before
// every chunk after the first (see chunkReader). Keys within a chunk
// go to whichever field is focused when the chunk arrives — so when a
// form spans multiple fields, make one Type call per field. A single
// call containing keys for several fields races huh's async field
// transition and can leak keys into the previous field.
func (h *Harness) Type(s string) {
	h.stdin.append(s)
}

// Run executes the bosun command tree with the given args. The
// workspace path is injected as --project so commands resolve config
// from the temp dir without depending on the test's CWD.
//
// Both cobra's cmd.OutOrStdout / cmd.ErrOrStderr streams AND the
// process-wide os.Stdout / os.Stderr file descriptors are captured
// for the duration of the call. The bosun rendering layer writes
// directly to os.Stdout via fmt.Print, so capturing only the cobra
// streams misses everything the user actually sees; tests can rely
// on h.Stdout() / h.Stderr() returning the full rendered output
// regardless of which channel each write went through.
func (h *Harness) Run(args ...string) error {
	h.t.Helper()

	// Each Run should behave like a fresh process invocation:
	// reset viper (so the file is re-read instead of returning cached
	// values from a prior Run) and reset the bootstrap guard (so
	// PersistentPreRunE actually runs Bootstrap again). Without this,
	// a set + get sequence within one test sees the set succeed on
	// disk but the get returns the pre-set value from the cached viper.
	viper.Reset()
	cli.ResetBootstrap()

	// Fresh-process semantics extend to the output buffers: Stdout()
	// documents "the most recent Run call", so prior runs' output
	// must not accumulate into this one's assertions.
	h.stdout.Reset()
	h.stderr.Reset()

	cmd := cli.NewRootCmd("test")
	cmd.SetIn(h.stdin)
	cmd.SetOut(h.stdout)
	cmd.SetErr(h.stderr)

	final := append([]string{}, args...)
	if !hasFlag(final, "--project") {
		final = append(final, "--project", h.Workspace.Dir)
	}
	cmd.SetArgs(final)

	restoreOut := captureFD(h.t, &os.Stdout, h.stdout)
	defer restoreOut()
	restoreErr := captureFD(h.t, &os.Stderr, h.stderr)
	defer restoreErr()

	return cmd.Execute()
}

// captureFD swaps *fd for a pipe whose reads are copied into sink for
// the duration of the returned restore func. Used to intercept the
// process-wide os.Stdout / os.Stderr during Run so direct fmt.Print
// writes from the rendering layer land in the harness's buffers
// alongside whatever cobra routes through its own streams.
func captureFD(t *testing.T, fd **os.File, sink io.Writer) func() {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("captureFD pipe: %v", err)
	}
	orig := *fd
	*fd = w

	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(sink, r)
		close(done)
	}()

	return func() {
		_ = w.Close()
		<-done
		*fd = orig
		_ = r.Close()
	}
}

// Stdout returns everything written to stdout during the most recent
// Run call — covers both cobra's cmd.OutOrStdout writes and direct
// fmt.Print writes from the rendering layer.
func (h *Harness) Stdout() string { return h.stdout.String() }

// Stderr returns everything written to stderr during the most recent
// Run call — covers both cobra's cmd.ErrOrStderr writes and direct
// fmt.Fprint(os.Stderr, ...) writes from the rendering layer.
func (h *Harness) Stderr() string { return h.stderr.String() }

// WorktreePath returns the canonical worktree path for a branch under
// the workspace's worktree root. Lifecycle commands lay worktrees out
// as {workspace.root}/{branch}/{repo}.
func (h *Harness) WorktreePath(branch, repoName string) string {
	root := viper.GetString("workspace.root")
	if root == "" {
		root = "workspaces"
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(h.Workspace.Dir, root)
	}
	return filepath.Join(root, branch, repoName)
}

// hasFlag reports whether args contains flag as a token. Only handles
// the long --flag and --flag=value forms — short -f flags would need
// extra logic we don't need yet (the harness only auto-injects --project,
// which has no short form). Add short-flag support when a test needs it.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// notInstalled returns a factory that fails the test if invoked.
// Used as the default for capability fakes the harness doesn't install
// itself, so a command that unexpectedly reaches for one surfaces the
// gap as a clear test failure instead of a nil-pointer panic.
func notInstalled[T any](t *testing.T, name string) func() (T, error) {
	return func() (T, error) {
		var zero T
		t.Fatalf("test invoked %s factory but no fake was installed", name)
		return zero, fmt.Errorf("no %s fake installed", name)
	}
}

// notInstalledPreview is the preview-provider variant of notInstalled
// (the signature differs because PreviewProvider takes a parameter).
func notInstalledPreview(t *testing.T) func(string) (preview.Provider, error) {
	return func(_ string) (preview.Provider, error) {
		t.Fatalf("test invoked PreviewProvider factory but no fake was installed")
		return nil, fmt.Errorf("no PreviewProvider fake installed")
	}
}

