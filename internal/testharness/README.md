# testharness

In-memory fixtures and a stream-injecting cobra harness for testing bosun
commands end-to-end without subprocesses, real network, or a real TTY.
Used by `internal/cli/*_test.go` to exercise full command paths — flag
parsing through context resolution through service calls through plan
confirmation through git operations — as a user would experience them.

## Quick start

```go
func TestStart_yes_flag_skips_prompt(t *testing.T) {
    h := testharness.New(t)
    h.Workspace.WriteConfig(`
repositories: ["repos/*"]
workspace:
  root: "workspaces"
issue_tracker:
  statuses:
    in_progress: "In Progress"
`)
    api := h.Workspace.AddRepo("api")
    h.Tracker.SeedIssue(issue.Issue{
        Key: "EX-1", Title: "Add feature", Type: "Story",
    })

    if err := h.Run("start", "--issue", "EX-1", "--slug", "feature", "--approve"); err != nil {
        t.Fatalf("start: %v", err)
    }
    if !api.HasBranch("story/EX-1_feature") {
        t.Errorf("branch missing")
    }
}
```

## Architecture

Four seams let the harness drive commands in-process:

1. **`cli.SetServices`** — swaps capability factories (Tracker, CodeHost,
   CICD, Notifier, PreviewProvider) for fakes. The harness installs them
   in `New()` and restores via `t.Cleanup`.
2. **`ui.SetStreams`** — wires `cmd.SetIn/SetOut/SetErr` through to
   `runForm` and the rendering layer. Forms read from the injected
   reader; output goes to the injected buffer. Triggered automatically
   by root.go's `PersistentPreRunE`.
3. **Real git on temp dirs** — `Workspace.AddRepo` creates a working
   repo plus a bare remote at `remotes/acme/<name>.git` so production
   code that does `git fetch origin <branch>` and `git push -u origin
   ...` works without modification. The origin URL is the GitHub-shaped
   `git@github.com:acme/<name>.git` — required by code that parses the
   remote for a repository identity (`gh.ParseRemote`) — while a
   `core.sshCommand` shim routes the transport to the local bare repo
   (see the gotcha below).
4. **Both config layers are scratch** — `XDG_CONFIG_HOME` points at a
   temp dir so the developer's real global config never leaks in.
   `Workspace.WriteConfig` writes the project layer;
   `Harness.WriteGlobalConfig` the global one (needed when the
   command reads the global config's presence or a setting has to
   come from that layer). `Harness.GlobalConfigPath` is the path it
   writes to.

## Test naming as scenario tree

Tests follow `TestX/<category>/<scenario>` so the `t.Run` hierarchy
doubles as a catalog. Example from `start_test.go`:

```
TestStart/
├── plan_confirmation/
│   ├── yes_flag_skips_prompt
│   ├── confirmed_applies
│   └── cancelled_aborts
├── repository_selection/
│   ├── filtered_by_flag
│   └── multi_repo_interactive_select
└── ...
```

`make test/tree` renders this hierarchy as a visual tree via
`cmd/gotree`. No separate scenario document is needed.

## Invariants — do not violate

### Tests cannot run in parallel

The harness mutates package-level globals (`cli.services`,
`ui.defaultInput/Output/Err`, `viper`, `config.ProjectRootOverride`,
`XDG_CONFIG_HOME`) under `t.Cleanup`-managed swaps. Calling
`t.Parallel()` in a test using the harness will race siblings and
fail nondeterministically. **Sequential is fine** — per-test setup is
single-digit hundreds of milliseconds.

### One Harness per leaf test

Build the harness inside each `t.Run(...)` body, not at the parent
function level. Sharing the harness across siblings leaks fake state.

### Uninstalled fakes fail loudly

`New()` installs `Tracker`, `Host`, and `Notifier`. The remaining
capability factories default to `t.Fatalf` stubs — if a command
unexpectedly reaches for CICD / PreviewProvider, the test fails with a
clear message instead of a nil-pointer panic. Tests for commands
needing those services opt in — `h.InstallCICD()` for CI/CD,
`h.InstallPreview()` for the preview provider. Each swaps its factory
for the fake and returns it.

The two differ in one respect worth knowing: `InstallPreview` allocates
a fresh `fakes.Preview` and assigns `h.Preview` (so seed *after*
installing), while `InstallCICD` installs the `h.CICD` that `New()`
already allocated (so knobs like `NewErr` can be set either side of the
call).

`PreviewProvider`'s opt-in is needed in any command whose path touches
a preview env — note that reaching for the *provider* is enough to
trip the stub, even when no env is bound, so e.g. every `cleanup` test
needs it (`cleanup` calls `newPreviewProvider` unconditionally and its
plan leads with the teardown row).

### When the collaborator IS the subject: `InstallPreviewAdapter`

`h.InstallPreviewAdapter()` points the PreviewProvider factory at
bosun's *production* adapter (`internal/preview/cicd`) rather than a
fake, so the provider composes the CI/CD pipeline and the tracker —
both already fakes. Workflow dispatches then land in
`h.CICD.Triggers()` with their real workflow path, ref, and inputs,
and the env-to-issue binding round-trips through `h.Tracker`'s issue
properties under the real key. Call `h.InstallCICD()` first.

Choose by what the command is *for*. `cleanup`, `status`, and
`review` merely consult a provider — they want the cheap, seedable
`fakes.Preview`. `preview` exists to resolve an issue key to an
environment and dispatch for it; a fake provider there can only
confirm that `Create` was called, and leaves the resolution itself
untested (see the key-binding note below).

The adapter probes `cicd.workflows.preview.url_template` over HTTP to
decide whether an env is alive, so a scenario that cares about
liveness points that template at an `httptest` server and expresses
"the env is up" as a 200 (404 = definitively gone, 500 = the
indeterminate probe `--force` exists for). See
`../cli/preview_e2e_test.go`. Without a template the adapter reports
every bound env as exists-but-unverifiable.

### Pin key bindings, then mutation-check them

A fake looked up under the WRONG key returns "nothing stored" — which
renders identically to a legitimate empty result. Scenarios that seed
state and then read their own seed back never exercise the binding at
all, so an entire suite can stay green while key resolution is
broken; the only visible symptom is a `(none)` where a value used to
be. `bosun status` shipped eleven scenarios with this hole.

Assert the key explicitly, using the fakes' key recorders —
`Tracker.GetPropertyKeys()`, `Tracker.Property(key)` — rather than
inferring it from downstream state. Then verify the assertion has
teeth: substitute a wrong constant for the key in the *production*
path and confirm the scenario fails. On the empty-registry path that
mutation changes nothing else observable, so a scenario without the
recorder assertion stays green.

### A regression that reaches an unexpected prompt HANGS

Worth knowing before you interpret a stuck test run. `chunkReader`
returns `io.EOF` once its queued input is drained, but bubbletea's
event loop doesn't terminate on input EOF — so a command that reaches
a prompt the scenario didn't feed blocks forever instead of failing.

That is a regression signal, not a harness bug per se, but it is an
expensive one: the hang burns the whole package's `go test` timeout
and takes every other `internal/cli` result down with it. Several
realistic regressions land here rather than on a clean failure — an
ignored `--approve`, an ignored `--dry-run`, a registry lookup that
silently misses (`preview` then falls through to its interactive
env-name prompt).

Two consequences:

- Run mutation checks with an explicit short `-timeout`; the default
  is 10 minutes per package.
- Where a scenario can make a skipped prompt fail *fast*, do it.
  Queueing the key that would produce the WRONG outcome turns the
  hang into an assertion failure — `preview`'s
  `plan_confirmation/yes_flag_skips_prompt` queues an `n` and asserts
  the deploy happened anyway, so a gate that stopped being suppressed
  reads the `n` and cancels instead of blocking.

  This only protects the run the key is queued for. A regression in
  something shared like `isAutoApprove` still hangs in whatever the
  fixture ran first (usually the `bosun start` that builds the
  workspace), so it buys a fast failure for the command under test,
  not immunity.

This predates any one command's suite (the same mutation hangs
`TestCleanup` and `TestPrerelease`). A watchdog in `Harness.Run`, or
aborting the form on EOF, would fix it centrally.

## Asserting on what a command reported

Commands run under a raw Reporter here, so the rendered timeline never
reaches `h.Stdout()` — see "Card output is invisible to the harness"
below for why. For a command whose entire output is Reporter calls
(`doctor`, `status`), assert on `h.Reporter` instead:

```go
if err := h.Run("doctor"); err != nil { ... }

ev, ok := h.Reporter.Find("code host")   // one check's row
// ev.Kind is ui.CaptureComplete / CaptureSkip / CaptureFail —
// the ✓ / ! / ✗ states — ev.Value the inline detail,
// ev.Group the enclosing Group's title.
```

`h.Reporter` is a `ui.CaptureReporter`: it records every Reporter
call in order (`Events()`), filters by kind (`OfKind`), finds by
label (`Find`), and renders itself for failure messages (`Dump()`).
It's reset at the start of each `Run`. Values may carry ANSI styling
where the command colored part of a string — strip with
`ansi.Strip` before comparing.

## Gotchas

### huh keypress conventions

Forms expect `bubbletea`'s key parser. When pre-filling stdin via
`h.Type(...)`, use:

| Field type        | Key sequence                                         |
|-------------------|------------------------------------------------------|
| Text input        | `<value>\r` (carriage return — `\n` does not submit) |
| Textarea (Text)   | `<value>\r` submits/advances; newline is `alt+enter` (`\x1b\r`) or `ctrl+j` — which is why a literal `\n` doesn't submit |
| Select            | `\x1b[B` = down, `\x1b[A` = up, `\r` accepts focused option |
| Confirm (Yes/No)  | `y` → affirmative, `n` → negative, `\r` → focused button (default = negative) |
| Multi-select      | `<space>` toggles, `\x1b[B` = down, `\x1b[A` = up, `\r` submits |

Plan confirmation gates use `huh.Confirm` with `Apply`/`Cancel` buttons
where Cancel is focused by default — drive with `h.Type("y")` to apply,
`h.Type("n")` to cancel.

### Multi-field forms: one Type call per field

huh advances fields asynchronously (the field emits `huh.NextField` as
a `tea.Cmd`; focus moves only when the resulting message round-trips
through the bubbletea runtime). Keys buffered past a field's final
Enter race that transition and can leak into the still-focused field.

`Harness.Type` therefore delivers each call as one chunk, pausing at
chunk boundaries until the transition has settled (see `chunkReader`).
Group the keys one focused field consumes per call:

```go
h.Type("Add audit log\r")          // title input
h.Type("Persist admin actions\r")  // description textarea
h.Type("\r")                       // type select (accept default)
```

A single call spanning several fields (`h.Type("a\rb\r")`) is the
racy shape — don't do it. Keys within one call are fine for a single
field's compound sequences (`" \x1b[B \r"` on a multi-select).

### GitHub-shaped remotes resolve through an ssh shim

`gh.ParseRemote` runs `git remote get-url origin` and only accepts
GitHub SSH/HTTPS URLs — a filesystem-path origin fails identity
resolution and the repo surfaces as a ✗ row. AddRepo therefore sets
origin to `git@github.com:acme/<name>.git` and points `core.sshCommand`
at a per-workspace shim that executes the pack command against
`remotes/` locally, so push/fetch never touch the network. (A
`url.<path>.insteadOf` rewrite does NOT work here: `git remote get-url`
expands it and hands the path to ParseRemote.) Host-side effects
(release tags cut by `fakes.Host.CreateRelease`) land on the bare
remote at `Repo.RemotePath`; assert with `Repo.HasTag` / seed with
`Repo.Tag`, which operate on the remote for exactly that reason.

### Sequential forms in one Run

bubbletea cannot cancel reads on a non-File reader, so each finished
form leaks a read-loop goroutine blocked in Read — and the
cancelreader fallback DISCARDS whatever a post-cancel read returns.
With chunks pre-queued, a command that runs two prompts in sequence
(target-selection form, then the plan gate) would have the first
form's leaked reader silently swallow the second prompt's keys and
hang the run. `ui.Input()` announces each form's start and
`ui.ReleaseInput()` its exit via the `InputHandoff` interface, and
`chunkReader` refuses delivery to reads that began under an earlier
session. The exit-side announcement makes the guarantee independent
of how much work a command does between prompts — the leaked read is
in flight before the form returns, so it's stale by construction.
This is automatic; it only means a chunk isn't consumed until the
form it's meant for is actually the live consumer.

### macOS symlinks + git worktree paths

`t.TempDir()` returns paths under `/var/folders/...`, which is a
symlink to `/private/var/folders/...`. `git worktree list --porcelain`
prints the resolved path. `Repo.WorktreeExists` handles this with
`filepath.EvalSymlinks` on both sides; if you write your own git-path
comparisons, do the same.

### Default-branch resolution needs a remote

`git.CreateBranch` runs `git rev-parse --abbrev-ref origin/HEAD` to
find the default branch before branching from it. A repo without an
origin will fail here. `Workspace.AddRepo` sets up a bare remote at
`remotes/<name>.git` and points origin/HEAD at main so production
code works unmodified.

### huh + non-TTY output requires explicit width

bubbletea's resize message never fires when output isn't a real
terminal, leading to a 0-width slice panic inside `textinput`.
`runForm` calls `huh.NewForm().WithWidth(ui.TermWidth())`
unconditionally to sidestep this. If you bypass `runForm` for some
reason, set width yourself.

### `Interactive()` is permissive by design

`ui.Interactive()` returns true for any non-`*os.File` reader — i.e.,
the test buffer counts as interactive even though no human is typing.
This is the test-injection escape hatch, not a security guarantee.
The only in-tree producer of non-File readers is this harness; if
that ever changes, the heuristic needs revisiting.

### Card output is invisible to the harness

`Bootstrap` picks the reporter from `ui.IsTerminal()`, and the
harness's output is a buffer — so every command runs under a **raw
reporter**, where `Card.Print` and the `RunCard*` helpers short-circuit
and draw nothing. `h.Stdout()` is therefore empty for any command whose
output is entirely cards (`status`, `doctor`, the lifecycle preambles).
Commands that write through `fmt.Fprint(ui.Output(), ...)` — `config
get`, and anything annotated `output: raw` — do surface there.

Nothing is *drawn*, but the semantic calls are still recorded: the raw
reporter the harness installs is a `ui.CaptureReporter`. Three
consequences when planning a command's scenarios:

- Assert on **what the command reported** through `h.Reporter` — see
  "Asserting on what a command reported" above. This reaches the
  package-level helpers too: `ui.Skip`, `ui.Fail`, `ui.Info` and
  `ui.Selected` all delegate to the active reporter, so a branch whose
  only effect is emitting one of them *is* assertable.
- Assert on **what the command asked for**: the fakes' `Calls()` logs,
  their recorded arguments, and post-run fake state — the other half
  of a read-only command's observable behavior.
- Cover section presence/absence by testing the card builders directly
  from `package cli` (see `../cli/status_test.go`). Splitting it this
  way also keeps the assertions off ANSI-styled strings.

What stays genuinely undrivable here, and shouldn't be chased when you
go looking for uncovered lines:

- **Interactive form-takeover branches are PTY-only.** Commands that
  morph a spinner program into a form (`review`, `prerelease`,
  `release`) fork on whether a frame was painted to take the cursor
  over from. Raw rendering never paints one, so the harness always
  takes the "build the form standalone" arm and the takeover arm —
  cursor arithmetic against that frame — is structurally undrivable.
  Don't restructure to chase it; there is nothing to assert without a
  terminal.
- **The raw reporter's own empty methods** (`../ui/raw_reporter.go`)
  never run under the harness at all, since the capture stands in for
  it. They stay uncovered by design, not for want of a test.

Forcing *actual* card rendering would still need a real PTY; nothing
in-tree does that, and the spinner frames would make output assertions
timing-dependent. The capture reporter sidesteps the problem by
recording the semantic calls instead of the pixels.

## Fake conventions

Capability fakes live in `fakes/` and follow a consistent shape:

- **Implement the production interface** verbatim. A compile-time
  `var _ issue.Tracker = (*Tracker)(nil)` line catches drift.
- **`Seed*` methods** pre-populate state (e.g., `SeedIssue`,
  `SeedBoard`). Tests build fixtures up-front, then run the command.
- **Read-only inspectors** expose state for assertions (e.g.,
  `Issues()`, `Calls()`). `Calls()` returns an ordered list of method
  names so tests can assert on the call sequence and idempotency.
- **Error knobs** (e.g., `CreateErr`, `GetErr`) let tests force the
  fake to return an error from a specific method, exercising the
  command's error path without complex setup.
- **`NewErr`** is the construction knob on the fakes whose factories
  the harness owns (`Tracker`, `Host`, `Notifier`, `CICD`): set it and
  the *factory* fails instead of handing out the fake, simulating a
  capability whose config or credentials are wrong. The harness's
  factory closures read it at call time, so it can be set any time
  before `Run`. This is the seam `doctor` uses to report a provider as
  configured-but-failing.
- **`sync.Mutex`** guards mutable state even though tests run
  sequentially — protects against subtle ordering issues if a fake
  is ever shared across goroutines.

When adding a new fake, look at `fakes/tracker.go` for the canonical
shape and copy the pattern.

## Adding a new command's tests

1. Read the command source in `internal/cli/<cmd>.go` to identify
   which `newXxx()` factories it calls.
2. Confirm each needed fake exists in `fakes/`. If not, add one
   following the `fakes/tracker.go` shape; expose it on `Harness` and
   install it in `New()`.
3. Create `internal/cli/<cmd>_test.go` with a top-level
   `Test<Cmd>` and a `t.Run("<category>/<scenario>", ...)` per case.
4. Build incrementally: get one scenario passing end-to-end before
   filling out the tree.
5. Verify via `go test ./internal/cli/ -run Test<Cmd> -v` and
   `make test/tree` to see the rendered scenario tree.

### Error-path coverage

An `errors/` (and `plan_confirmation/`) group is only complete when it
exercises the failure modes the command actually has. Two are easy to
miss because the happy path never touches them:

- **Apply-stage failure.** For a command that mutates, force the
  relevant fake's error knob (`CreateErr`, `SetStatusErr`, the code-host
  / cicd equivalents) and assert the error surfaces AND the mutation
  didn't half-land (zero created issues, no branch, etc.). The apply
  error propagates straight out of `runActions` as the command's return
  — see `create_test.go:errors/create_failure_surfaces`. Read-only
  commands (`status`, `doctor`) have no apply stage; their analogue is a
  fetch/probe failure (`GetErr`, a provider probe error), not this.
- **Plan-gate cancel.** Typing `n` at the confirmation gate returns
  `ErrCancelled` with no mutations — distinct from `--dry-run` (same
  no-mutation outcome, different trigger). See
  `create_test.go:plan_confirmation/cancelled_aborts`.

The seam is the fake's error knobs (see Fake conventions above); reach
for them rather than constructing elaborate failing fixtures.

## Files

| File                          | Purpose                                          |
|-------------------------------|--------------------------------------------------|
| `harness.go`                  | `Harness` + `New(t)` entry point                 |
| `workspace.go`                | `Workspace` + `Repo` fixtures                    |
| `input.go`                    | `chunkReader` stdin (chunk pacing + sessions)    |
| `fakes/tracker.go`            | In-memory `issue.Tracker` (canonical fake)       |
| `fakes/host.go`               | In-memory `code.Host` (real tags via LinkRepo)   |
| `fakes/notifier.go`           | In-memory `notify.Notifier`                      |
| `fakes/preview.go`            | In-memory `preview.Provider` (opt-in install)    |
| `fakes/cicd.go`               | In-memory `cicd.CICD` (opt-in install)           |
| `fakes/...`                   | Additional capability fakes (added as needed)    |
| `../cli/start_test.go`        | Canonical end-to-end test example                |
| `../cli/doctor_test.go`       | Canonical `h.Reporter` assertion example         |
