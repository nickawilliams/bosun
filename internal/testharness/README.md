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

Three seams let the harness drive commands in-process:

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
needing those services install them by calling `cli.SetServices`
directly after `New()`.

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
hang the run. `ui.Input()` announces each new form via the
`InputHandoff` interface and `chunkReader` refuses delivery to reads
that began under an earlier session. This is automatic; it only means
a chunk isn't consumed until the form it's meant for is actually the
live consumer.

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
| `fakes/...`                   | Additional capability fakes (added as needed)    |
| `../cli/start_test.go`        | Canonical end-to-end test example                |
