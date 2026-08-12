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
	"time"

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

	// Preview is the in-memory preview provider, nil until a test calls
	// InstallPreview. Not installed by default — see that method.
	Preview *fakes.Preview

	// CICD is the in-memory CI/CD fake. It is NOT installed by
	// default — the CICD factory fails the test until InstallCICD is
	// called, so a command that reaches for CI/CD unexpectedly still
	// surfaces the gap. Its knobs are read at call time, so a test can
	// set them either side of the install.
	CICD *fakes.CICD

	// Reporter records every ui.Reporter call the command made, so
	// tests can assert on reported steps without parsing styled
	// terminal output (commands run in raw mode under the harness —
	// output isn't a TTY — so the rendered timeline never reaches
	// Stdout()). Reset at the start of each Run.
	Reporter *ui.CaptureReporter

	// globalConfigDir is $XDG_CONFIG_HOME/bosun for this test.
	globalConfigDir string

	// fatalf is t.Fatalf, indirected so this package's own tests can
	// assert on a starvation report without ending the test that
	// provoked it.
	fatalf func(format string, args ...any)

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
// those services install fakes after calling New — PreviewProvider via
// the InstallPreview helper, CICD via InstallCICD.
func New(t *testing.T) *Harness {
	t.Helper()
	h := &Harness{
		t:         t,
		Workspace: NewWorkspace(t),
		Tracker:   fakes.NewTracker(),
		Host:      fakes.NewHost(),
		Notifier:  fakes.NewNotifier(),
		CICD:      fakes.NewCICD(),
		Reporter:  ui.NewCaptureReporter(),
		stdin:     newChunkReader(),
		stdout:    &syncBuffer{},
		stderr:    &syncBuffer{},
	}
	h.fatalf = t.Fatalf

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

	// Commands run non-interactively here (output is a buffer, not a
	// TTY), so cli.Bootstrap installs a raw Reporter that renders
	// nothing — and re-installs one on every Run. Swapping the
	// raw-mode constructor rather than the installed Reporter is what
	// makes the capture survive that.
	t.Cleanup(ui.SetRawReporterFactory(func() ui.Reporter { return h.Reporter }))

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
	h.globalConfigDir = filepath.Join(xdg, "bosun")

	// Each factory consults its fake's NewErr at call time, so a test
	// can make construction fail after New() — the seam for "this
	// capability is misconfigured" without swapping the whole
	// Services set.
	cli.SetServices(&cli.Services{
		IssueTracker: func() (issue.Tracker, error) {
			if h.Tracker.NewErr != nil {
				return nil, h.Tracker.NewErr
			}
			return h.Tracker, nil
		},
		CodeHost: func() (code.Host, error) {
			if h.Host.NewErr != nil {
				return nil, h.Host.NewErr
			}
			return h.Host, nil
		},
		CICD: notInstalled[cicd.CICD](t, "CICD"),
		Notifier: func() (notify.Notifier, error) {
			if h.Notifier.NewErr != nil {
				return nil, h.Notifier.NewErr
			}
			return h.Notifier, nil
		},
		PreviewProvider: notInstalledPreview(t),
	})

	return h
}

// InstallPreview swaps the fail-loudly PreviewProvider factory for an
// in-memory fake, sets h.Preview, and returns it. Call it right after
// New for commands that reach for a preview provider (cleanup tears an
// env down as the first row of its plan; preview and review consult
// one).
//
// Kept opt-in rather than installed by New so a command that touches
// previews unexpectedly still fails loudly for every other test — the
// same posture the CICD factory keeps.
//
// Installs by copy-and-swap with its own restore rather than writing
// the field on the live *Services in place. In-place mutation happens
// to be safe today only because New always installs a fresh struct
// immediately beforehand; the moment anything puts a shared *Services
// in place between the two calls, an in-place write would outlive this
// test with nothing to undo it — leaving a finished harness's fake
// bound to later tests.
func (h *Harness) InstallPreview() *fakes.Preview {
	h.t.Helper()
	h.Preview = fakes.NewPreview()

	prev := cli.GetServices()
	next := *prev
	next.PreviewProvider = func(string) (preview.Provider, error) {
		return h.Preview, nil
	}
	cli.SetServices(&next)
	h.t.Cleanup(func() { cli.SetServices(prev) })

	return h.Preview
}

// InstallCICD swaps the fail-loudly CICD factory for the in-memory
// fake and returns it. Opt-in for the same reason InstallPreview is:
// commands that shouldn't touch CI/CD keep the guard, and commands
// that should say so explicitly. Installs by copy-and-swap with its
// own restore for the reason spelled out on InstallPreview.
//
// Unlike InstallPreview, this does NOT allocate a fresh fake — h.CICD
// is created by New and installed as-is, so a test can set its knobs
// (NewErr, TriggerErr) before installing and they survive. The
// installed factory also consults NewErr at call time, so forcing
// construction to fail after installing works too.
func (h *Harness) InstallCICD() *fakes.CICD {
	h.t.Helper()

	prev := cli.GetServices()
	next := *prev
	next.CICD = func() (cicd.CICD, error) {
		if h.CICD.NewErr != nil {
			return nil, h.CICD.NewErr
		}
		return h.CICD, nil
	}
	cli.SetServices(&next)
	h.t.Cleanup(func() { cli.SetServices(prev) })

	return h.CICD
}

// WriteGlobalConfig writes yaml to the scratch $XDG_CONFIG_HOME's
// bosun/config.yaml — the global half of bosun's two-layer config.
// Tests need it when the command under test inspects the global
// config's presence (bosun doctor) or when a setting must come from
// the global layer rather than the project's.
func (h *Harness) WriteGlobalConfig(yaml string) {
	h.t.Helper()
	if err := os.MkdirAll(h.globalConfigDir, 0o755); err != nil {
		h.t.Fatalf("create global config dir: %v", err)
	}
	path := filepath.Join(h.globalConfigDir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		h.t.Fatalf("write global config: %v", err)
	}
}

// GlobalConfigPath returns the path WriteGlobalConfig writes to,
// whether or not the file exists — the value bosun doctor reports for
// the global config check.
func (h *Harness) GlobalConfigPath() string {
	return filepath.Join(h.globalConfigDir, "config.yaml")
}

// InstallPreviewAdapter points the PreviewProvider factory at bosun's
// *production* adapter (internal/preview/cicd) instead of a fake, so
// the provider under test is the real composition of the CI/CD
// pipeline and the issue tracker — both of which are already fakes
// here. Install the CICD fake too: the adapter reaches for the
// pipeline through the same factory the command does, and without one
// that factory fails the test. Order doesn't matter — the lookup
// happens when the provider is built, not when this is called.
//
// Use this for `bosun preview`, where the provider is not a
// collaborator but the thing being exercised: the workflow dispatches
// land in h.CICD.Triggers() with their real workflow path, ref, and
// inputs, and the env-to-issue binding round-trips through
// h.Tracker's issue properties under the real issue key. A fake
// provider there would only be able to confirm that the command
// called Create — and a test that seeds an environment and then reads
// its own seed back never pins the key binding at all.
//
// Prefer InstallPreview (the fake) for commands that merely consult a
// provider — cleanup, status, review. Those want a cheap, seedable
// environment, not the adapter's workflow machinery.
//
// The adapter probes cicd.workflows.preview.url_template over HTTP to
// decide whether an env is alive, so a test that cares about liveness
// must point that template at an httptest server; without a template
// the adapter reports every bound env as exists-but-unverifiable.
func (h *Harness) InstallPreviewAdapter() {
	h.t.Helper()

	prev := cli.GetServices()
	next := *prev
	next.PreviewProvider = cli.DefaultServices().PreviewProvider
	cli.SetServices(&next)
	h.t.Cleanup(func() { cli.SetServices(prev) })
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
// from the temp dir without depending on the test's CWD — unless the
// test called Workspace.Uninitialize, in which case there is no
// project to point at yet and the run goes bare (see below).
//
// Both cobra's cmd.OutOrStdout / cmd.ErrOrStderr streams AND the
// process-wide os.Stdout / os.Stderr file descriptors are captured
// for the duration of the call. The bosun rendering layer writes
// directly to os.Stdout via fmt.Print, so capturing only the cobra
// streams misses everything the user actually sees; tests can rely
// on h.Stdout() / h.Stderr() returning the full rendered output
// regardless of which channel each write went through.
//
// A command that reaches a prompt the scenario never fed fails the
// test here rather than hanging: the drained reader unblocks the form
// with errStarvedInput after starvationGrace, and Run reports the
// starvation whether or not the command forwarded the error.
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
	h.Reporter.Reset()
	h.stdin.beginRun()

	cmd := cli.NewRootCmd("test")
	cmd.SetIn(h.stdin)
	cmd.SetOut(h.stdout)
	cmd.SetErr(h.stderr)

	final := append([]string{}, args...)
	// --project only works against an already-initialized project —
	// Bootstrap rejects a directory with no .bosun/ — so a workspace a
	// test deliberately uninitialized (bosun init's fresh-project
	// scenarios) runs bare and resolves its project from the working
	// directory instead.
	if !hasFlag(final, "--project") && h.Workspace.Initialized() {
		final = append(final, "--project", h.Workspace.Dir)
	}
	cmd.SetArgs(final)

	restoreOut := captureFD(h.t, &os.Stdout, h.stdout)
	defer restoreOut()
	restoreErr := captureFD(h.t, &os.Stderr, h.stderr)
	defer restoreErr()

	err := cmd.Execute()

	// Restore before diagnosing: the starvation report quotes the
	// captured output, and the pipe's copy goroutine is only guaranteed
	// to have drained once its writer is closed. Both restores are
	// idempotent, so the defers above stay as the panic path.
	restoreErr()
	restoreOut()

	// The command has returned, so any read still parked belongs to a
	// finished form's leaked loop — retire it rather than let it sit
	// out the grace and latch a starvation this run never suffered.
	h.stdin.release()

	if starved, queued, grace := h.stdin.starvation(); starved {
		h.fatalf("%s", h.starvationReport(final, queued, grace))
	}
	return err
}

// starvationReport explains a starved run: which invocation blocked,
// how much input the scenario had thought would cover it, and what was
// on screen when it stopped.
//
// The trailing output is the part that names the prompt. Prompts are
// preceded by a rewindable input card (ui.CardInput) written straight
// to the output buffer, and a buffer keeps what a terminal would have
// erased — so the last card in the capture is the question the command
// is sitting on. Rendering the prompt itself would not help: with a
// non-TTY output bubbletea uses the nil renderer and the form draws
// nothing at all.
func (h *Harness) starvationReport(args []string, queued int, grace time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "bosun %s reached a prompt this scenario never fed.\n\n",
		strings.Join(args, " "))
	fmt.Fprintf(&b, "The scenario queued %d input(s) via Type(); the command "+
		"consumed all of them and asked for more, so the harness aborted the "+
		"form after %s instead of blocking until the package test timeout.\n\n",
		queued, grace)
	b.WriteString("Either feed the prompt with another h.Type(...) call, or — if " +
		"the command should not be prompting here at all — that is the " +
		"regression under test.\n\n")

	if tail := outputTail(h.Stdout(), starvationTailLines); tail != "" {
		fmt.Fprintf(&b, "Last output before it blocked (the prompt is usually the "+
			"final card):\n%s", tail)
	} else {
		b.WriteString("The command produced no output before it blocked.")
	}
	return b.String()
}

// starvationTailLines is how much trailing output the starvation
// report quotes — enough for the last input card plus the row or two
// of context above it that says which step the command was on.
const starvationTailLines = 12

// outputTail returns the last n non-blank lines of s with ANSI escapes
// stripped and each line indented, for quoting inside a test failure.
func outputTail(s string, n int) string {
	var lines []string
	for line := range strings.SplitSeq(stripANSI(s), "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString("    ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// stripANSI removes ANSI escape sequences so the quoted output reads
// as plain text in a test failure. Handles the CSI and OSC forms the
// rendering layer emits (colors, cursor moves, erases) plus the plain
// two- and three-byte escapes (charset designators and the like).
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // ESC
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: params, then a final byte in @-~
			i++
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			if i < len(s) {
				i++
			}
		case ']': // OSC: runs to BEL or ST (ESC \)
			i++
			for i < len(s) && s[i] != 0x07 {
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
				i++
			}
			if i < len(s) {
				i++
			}
		default:
			// Everything else: zero or more intermediates (0x20-0x2F)
			// then one final byte — ESC(B, ESC)0, ESC M, ESC =.
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
				i++
			}
			if i < len(s) {
				i++
			}
		}
	}
	return b.String()
}

// captureFD swaps *fd for a pipe whose reads are copied into sink for
// the duration of the returned restore func. Used to intercept the
// process-wide os.Stdout / os.Stderr during Run so direct fmt.Print
// writes from the rendering layer land in the harness's buffers
// alongside whatever cobra routes through its own streams.
//
// The restore is idempotent, so Run can both call it explicitly (to
// settle the capture before reading it back) and defer it (to survive
// a panic in the command).
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

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = w.Close()
			<-done
			*fd = orig
			_ = r.Close()
		})
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
