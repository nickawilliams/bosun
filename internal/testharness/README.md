# testharness

In-memory fixtures and a stream-injecting cobra harness for testing bosun
commands end-to-end without subprocesses, real network, or a real TTY.
Used by `internal/cli/*_test.go` to exercise full command paths — flag
parsing through context resolution through service calls through plan
confirmation through git operations — as a user would experience them.

> **Editing this document.** Sections are scoped by concern so that two
> people documenting two different mechanisms touch two different
> sections. Add a new `###` under the `##` that owns your concern rather
> than extending someone else's prose, and prefer appending at the end of
> a section over restructuring it. This file has produced merge conflicts
> that git resolved *cleanly* and *wrongly* — twice, once leaving a
> factually false claim in place — because several authors were editing
> one shared narrative. If a rebase touches this file, re-read the merged
> section rather than trusting a conflict-free merge.

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
   (see Environment gotchas).
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

Three rules about how a harness test is *constructed*. Everything else
in this document is guidance; these produce nondeterministic failures
or silent state leaks when broken.

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

## Choosing fakes vs the production adapter

`h.InstallPreviewAdapter()` points the PreviewProvider factory at
bosun's *production* adapter (`internal/preview/cicd`) rather than a
fake, so the provider composes the CI/CD pipeline and the tracker —
both already fakes. Workflow dispatches then land in
`h.CICD.Triggers()` with their real workflow path, ref, and inputs,
and the env-to-issue binding round-trips through `h.Tracker`'s issue
properties under the real key. Install the CICD fake too — the
adapter resolves the pipeline through the same factory the command
does, and without one that factory fails the test. Order doesn't
matter.

Choose by what the command is *for*. `cleanup`, `status`, and
`review` merely consult a provider — they want the cheap, seedable
`fakes.Preview`. `preview` exists to resolve an issue key to an
environment and dispatch for it; a fake provider there can only
confirm that `Create` was called, and leaves the resolution itself
untested (see "Pin key bindings" under Writing assertions).

The adapter probes `cicd.workflows.preview.url_template` over HTTP to
decide whether an env is alive, so a scenario that cares about
liveness points that template at an `httptest` server and expresses
"the env is up" as a 200 (404 = definitively gone, 500 = the
indeterminate probe `--force` exists for). See
`../cli/preview_e2e_test.go`. Without a template the adapter reports
every bound env as exists-but-unverifiable.

## Writing assertions

Everything about *what is observable* lives here. The short version:
three surfaces — `h.Reporter`, `h.Stdout()`, and the fakes — plus one
category that is genuinely undrivable and shouldn't be chased.

### What reaches `h.Reporter`

`Bootstrap` picks the reporter from `ui.IsTerminal()`, and the
harness's output is a buffer — so every command runs under a **raw
reporter**, where `Card.Print` and the `RunCard*` helpers short-circuit
and draw nothing.

Nothing is *drawn*, but the semantic calls are still recorded: the raw
reporter the harness installs is a `ui.CaptureReporter`. For a command
whose entire output is Reporter calls (`doctor`, `status`), assert on
`h.Reporter`:

```go
if err := h.Run("doctor"); err != nil { ... }

ev, ok := h.Reporter.Find("code host")   // one check's row
// ev.Kind is ui.CaptureComplete / CaptureSkip / CaptureFail —
// the ✓ / ! / ✗ states — ev.Value the inline detail,
// ev.Group the enclosing Group's title.
```

`h.Reporter` records every Reporter call in order (`Events()`), filters
by kind (`OfKind`), finds by label (`Find`), and renders itself for
failure messages (`Dump()`). It's reset at the start of each `Run`.
Values may carry ANSI styling where the command colored part of a
string — strip with `ansi.Strip` before comparing.

This reaches the package-level helpers too: `ui.Skip`, `ui.Fail`,
`ui.Info` and `ui.Selected` all delegate to the active reporter, so a
branch whose only effect is emitting one of them *is* assertable.

### What reaches `h.Stdout()`

**"`h.Stdout()` is empty" is about cards, not about stdout.** The
shorthand has been over-applied and it costs real assertions, so state
the boundary precisely: what short-circuits is `Card.Print` and the
`RunCard*` helpers, because those consult the reporter. Anything that
writes to the process's stdout directly still lands in `h.Stdout()` —
`Harness.Run` swaps `os.Stdout` for a captured pipe (`captureFD`), so
the bare `fmt.Print`/`fmt.Printf` family in `../ui/output.go` reaches
the buffer whatever the reporter is doing.

`ui.SuccessLine` is the one that matters in practice: it's how `config
set`, `config unset` and `init` confirm *where* they wrote, it is not a
Reporter call, and it is therefore assertable **only** through
`h.Stdout()` — `init_test.go`'s
`fresh_project/writes_config_with_minimum_fields` does exactly that.
`ui.EmptyState` and `ui.Bold` are the same shape. `config get`, and
anything annotated `output: raw`, also surface there.

Two practical notes when you assert there. Live `huh` forms also render
through `ui.Output()`, so for a prompt-driven command the buffer is
mostly bubbletea's ANSI — match substrings rather than whole lines, and
run it through `ansi.Strip` first, since each styled segment carries
its own escapes. And the useful negative — "this line was NOT
printed" — only means something once the positive case is pinned
somewhere, for the usual reason.

### What reaches the fakes

Assert on **what the command asked for**: the fakes' `Calls()` logs,
their recorded arguments, and post-run fake state — the other half
of a read-only command's observable behavior.

Cover section presence/absence by testing the card builders directly
from `package cli` (see `../cli/status_test.go`). Splitting it this
way also keeps the assertions off ANSI-styled strings.

### Pin key bindings, then mutation-check them

A fake looked up under the WRONG key returns "nothing stored" — which
renders identically to a legitimate empty result. Scenarios that seed
state and then read their own seed back never exercise the binding at
all, so an entire suite can stay green while key resolution is
broken; the only visible symptom is a `(none)` where a value used to
be. `bosun status` shipped eleven scenarios with this hole.

Assert the key explicitly, using the fakes' key recorders —
`Tracker.GetIssueKeys()`, `Tracker.GetPropertyKeys()`,
`Tracker.Property(key)`, `Preview.GetKeys()`, `Host.ChecksRefs()` —
rather than inferring it from downstream state. Then verify the assertion has
teeth: substitute a wrong constant for the key in the *production*
path and confirm the scenario fails. On the empty-registry path that
mutation changes nothing else observable, so a scenario without the
recorder assertion stays green.

The same trap has a second shape: an assertion whose expected value
also happens to be the code's default or fallback constant cannot
fail. And a scenario asserting only on values already present in its
own fixture before the command ran can catch a *wrong* write but never
*no write at all*, because the correct end state is byte-identical to
the seed — see `init_test.go`'s `quick_flag/*`, which needs both a
rewrite guard and a dry-run scenario to close that shape. When a
mutation survives, treat it as a question about the assertion before
concluding the code is right.

### What is genuinely undrivable

Don't chase these when you go looking for uncovered lines:

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

## Driving forms

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

Three consequences:

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

- Pick a sentinel that lets the run **succeed**, not one that produces
  another cancellation. For a scenario whose subject *is* cancellation
  (`init`'s `errors/*`), a trailing second `\x03` looks like the
  obvious tripwire and is the wrong choice: a swallowed abort would
  simply cancel at the next prompt, the run would still end in
  `ErrCancelled`, and every assertion would still hold — masking the
  exact bug the sentinel was added for. Queue the keys that would carry
  the run to a clean finish instead, so the error assertion reports
  `err = nil`. Verified on `init`: with the discriminating sentinel a
  swallowed-abort mutation fails in ~1s; without it the same mutation
  ran until the 25s `-timeout` killed the package.

This predates any one command's suite (the same mutation hangs
`TestCleanup` and `TestPrerelease`). A watchdog in `Harness.Run`, or
aborting the form on EOF, would fix it centrally — tracked in #59.

## Command-specific notes

Behaviour peculiar to one command, where a scenario author would
otherwise have to rediscover it. Add a `###` per command.

### `bosun init` writes against the CWD, but still reads `--project`

Every other command resolves its project through the `--project` flag
`Run` injects. `init` decides *where to write* differently: it creates
`.bosun/` relative to `os.Getwd()`. A scenario for it therefore has to
`t.Chdir(h.Workspace.Dir)` — which is safe here for the same reason
`t.Setenv` is, since harness tests never run in parallel.

It does not *ignore* the flag, though, and the difference is
load-bearing. `newInitCmd` calls `addProjectFlag`, and the
nested-project guard asks `config.FindProjectRoot()` whether it is
already inside a project — which returns `config.ProjectRootOverride`
outright when `--project` set one, and only walks up from the CWD when
it didn't. So `init` ignores `--project` for where it writes and
consults it for whether it refuses to write at all. A scenario that
means to exercise the guard's real (upward-walk) path must therefore
run with the workspace uninitialized so `Run` injects no `--project`,
and build the enclosing project by hand somewhere under it; with the
flag injected the guard fires on the override and the walk is never
reached. `init_test.go`'s `already_initialized/nested_project_rejected`
is laid out that way for exactly this reason.

`--project` is also useless before a project exists: `Bootstrap`
rejects a directory with no `.bosun/`. So `Workspace.Uninitialize()`
removes the `.bosun/` that `NewWorkspace` creates for everyone else's
benefit (init would read it as a reinit), and `Run` skips the
`--project` injection while the workspace stays that way — see
`Workspace.Initialized`. `Workspace.ReadConfig` / `ConfigPath` are the
round-trip counterparts for asserting on what got written.

Two things about init's prompt flow are worth knowing before writing
its scenarios, because both cost a keystroke that isn't about the
thing under test. The optional-service wizard runs whenever the
session is interactive — which the harness always is — so every
non-`--quick` run ends with four provider gates that each need a `\r`.
And `resolveGroupAsForm`'s fields arrive pre-filled with the schema's
example rather than showing it as a placeholder, so typing appends;
send `\x15` (ctrl+u) first to clear the field.

The two input shapes are easy to mix up, so both are now pinned by
`integration_groups/form_fields_arrive_prefilled`. Confirmed by
mutation: drop the `\x15` and `issue_tracker.base_url` comes back as
`https://mycompany.atlassian.nethttps://acme.test` — the example with
the typed value glued on. The project-settings prompts
(`newDefaultInput`) are the *opposite* shape — the default is a
placeholder, so a bare `\r` accepts it and typing replaces rather than
appends. A field left untouched in a `resolveGroupAsForm` form persists
its prefill as a real configured value, which is what makes clearing
matter.

`--quick` trims the four gates (each group goes through `resolveGroup`,
which prompts only for missing required keys) but does NOT trim the
repository-patterns and workspace-root prompts — its reuse of existing
values is conditional on the run being a reinit. On a fresh project,
`--quick` still asks for both.

Since the whole command is prompts, a scenario that expects *not* to
be asked something has no clean way to fail — it hangs. Queue a
trailing `h.Type("\x03")` as a tripwire in those cases, subject to the
sentinel caveat under Driving forms: nothing reads it on the intended
path, and a regression that reaches an unplanned prompt aborts with
`ErrCancelled` instead of eating the package timeout. See
`../cli/init_test.go`.

## Environment gotchas

Things about git, the OS, and the absence of a TTY that bite
regardless of which command you're testing.

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
3. Create `internal/cli/<cmd>_test.go` in `package cli_test` with a
   top-level `Test<Cmd>` and a `t.Run("<category>/<scenario>", ...)`
   per case. When that file name is already taken by in-package
   (`package cli`) unit tests, use `<cmd>_e2e_test.go` — Go allows
   both packages in one directory, but not in one file. Prefix shared
   helpers with the command name (`startPrereleaseWorkspace`,
   `cleanupConfig`): every E2E file shares one package scope.
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
