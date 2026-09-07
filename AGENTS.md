# Agent Guidelines

Instructions for AI coding agents working on this project.

## UI Architecture

Bosun uses a **Card timeline** for all command output. Two APIs render into
the timeline, with a clear boundary between them:

- **Reporter** (`internal/ui/reporter.go`) — declarative output. Use for
  messages, step results, structured data, and async tasks. Commands call
  the package-level helpers in `output.go` and `steps.go` which delegate
  to the default Reporter.
- **Card** (`internal/ui/card.go`) — stateful interaction. Use for forms
  (`CardInput` + `PrintRewindable`), plan lifecycle (`PlanCard`), animated
  spinners (`RunCard`/`RunCardReplace`), and root headers (`CardRoot`).

**Default to Reporter.** Only use Card directly when the output requires
state transitions, user input, or animation.

### Reporter (declarative output)

Use these package-level helpers in command `RunE` functions:

| Helper                           | When to use                              |
|----------------------------------|------------------------------------------|
| `ui.Complete(label)`             | Step finished successfully               |
| `ui.CompleteWithDetail(l, items)`| Step finished with detail lines          |
| `ui.Skip(label)`                 | Step not attempted (missing config, etc) |
| `ui.Fail(label)`                 | Step attempted and failed                |
| `ui.Success(fmt, args)`          | Positive confirmation message            |
| `ui.Info(fmt, args)`             | Informational message                    |
| `ui.Warning(fmt, args)`          | Cautionary message                       |
| `ui.Saved(label, value)`         | Value was persisted                      |
| `ui.DryRun(fmt, args)`           | Dry-run notice                           |
| `ui.Details(heading, fields)`    | Key-value block with heading             |
| `ui.RunCard(title, fn)`          | Async task with spinner                  |

Build fields with `ui.NewFields("key", "value", ...)` or by constructing
`ui.Fields` directly.

### Card (stateful interaction)

Use Card directly only for these patterns:

| Pattern                | Example                                             |
|------------------------|-----------------------------------------------------|
| Root header            | `rootCard(cmd, issue).Print()`                      |
| Form input             | `ui.NewCard(ui.CardInput, label).PrintRewindable()`  |
| Plan lifecycle         | `ui.NewPlan()` + `runPlanCard(cmd, plan, actions)`  |
| Spinner with replace   | `ui.RunCardReplace(title, fn, successCard)`         |
| Rich result card       | Card with `.Subtitle()` + `.KV()` body combinations |

### Card state semantics

- `CardSuccess` — operation completed successfully
- `CardFailed` — operation was attempted and returned an error
- `CardSkipped` — operation was not attempted (missing config, optional
  dependency unavailable, precondition unmet)
- `CardInfo` — informational display, not an operation result

### Title case for card titles

Use title case for all card titles, action labels, and spinner messages
(e.g., "Trigger Preview Deploy", "Fetching Boards", "Select Board").

### Follow established patterns

When adding new code to an existing flow, check how the surrounding code
handles the same concern (prompting, error handling, config access, etc.)
and follow that pattern by default. Only deviate with an explicit reason.

### What not to do

- Do not use `fmt.Print*` directly in command `RunE` functions. Use
  Reporter helpers or Card.
- Do not build aligned text with `fmt.Sprintf("%-12s ...")`. Use
  `ui.Details()` with `ui.Fields` or `Card.KV()`.
- Do not create `lipgloss.NewStyle()` in `internal/cli/` files. Reference
  `ui.Palette` colors from `internal/ui/` instead.
  Exception: `help.go` creates help-specific styles because help output
  is a static text block, not part of the Card timeline.
- Do not call `ui.Bold()`, `ui.Item()`, or `ui.Error()` from command
  `RunE` functions. These are legacy helpers for non-timeline contexts.

### Reference implementations

When adding a new command, use these as models:

- **Root header**: `internal/cli/header.go` — `rootCard(cmd, context...)`
- **Service error handling**: `internal/cli/review.go` — `ui.Skip()` for
  unavailable services, `ui.Fail()` for operation errors
- **Async fetch**: `internal/cli/start.go` — `ui.RunCard()` with spinner
- **Plan lifecycle**: `internal/cli/review.go` — plan + confirm + apply
- **Form input**: `internal/cli/init.go` — `CardInput` + `PrintRewindable`
- **Structured data**: `internal/cli/status.go` — `ui.Details()` with fields

## Project Conventions

- **No local state files.** Issue tracker is the source of truth. Everything
  is queried from providers at runtime.
- **Idempotent actions.** Commands should be safe to re-run.
- **Multi-repository fan-out.** Lifecycle commands operate on all configured repositories.
- **Config resolution.** Global config merges under project config. Env vars
  with a `BOSUN_` prefix (or a key's explicit `EnvVar`, e.g. `GITHUB_TOKEN`)
  override both, through explicit per-key `viper.BindEnv` registration:
  `bindSchemaEnv` (cli layer) registers every scalar schema key right after
  `config.Load`, so a bare `viper.Get*` resolves env → project → global →
  default. Map-shaped groups are addressed at the group key and take the
  whole map as JSON, decoded by `mapGroupValues` rather than registered with
  viper (a bound name shadows the group's children in `AllKeys`, which the
  display path renders from). Do not add `AutomaticEnv` back — it shadowed
  whole config blocks (see the comment in `config.Load`) and does not compose
  with `BindEnv`.

## GitHub Conventions

### Issue Titles

Issue titles become slugs in branch names, PR titles, and URLs — keep
them short so the generated slugs stay a reasonable length.

- **3 words ideal, ~5 words as a soft cap.** Going slightly over is fine
  when forcing fewer words would mangle the meaning; the goal is short
  slugs, not a strict count.
- **Title Case** (e.g., "Refine Command Output", "Improve Timeline
  Termination").
- No scope prefixes (`cli:`, `ui:`) and no trailing punctuation; the
  issue body carries the detail.

## Versioning

Releases are cut automatically by git-cliff from conventional commit
types (`.github/workflows/release.yaml`, `cliff.toml`). The bump is
computed from commit messages alone — nothing else votes.

### Commit types

The type decides whether a commit reaches the release notes, so pick it
for the audience, not the diff. Nothing validates it — a type outside
this table is silently dropped from the changelog with no error, so a
typo costs the entry.

| Type       | Use for                                          | Release notes   |
|------------|--------------------------------------------------|-----------------|
| `feat`     | New user-facing capability                        | New Features    |
| `fix`      | Corrected behavior                                | Fixes           |
| `refactor` | Internal restructuring, behavior unchanged        | Improvements    |
| `perf`     | Faster, behavior unchanged                        | Improvements    |
| `style`    | **Appearance of command output**                  | Appearance      |
| `build`    | Build, tooling, dependencies, **and CI**          | skipped         |
| `test`     | Tests and harness                                 | skipped         |
| `docs`     | Documentation                                     | skipped         |
| `chore`    | Maintenance with no user-visible effect, incl. code formatting | skipped |

Two conventions here differ from the Angular defaults; both are
deliberate.

**`style` is about what the user sees, not how the code is laid out.**
It has always meant terminal appearance in this repo — glyphs, color,
spacing, card layout. Those are user-visible, so they render under
**Appearance**. Pure code formatting (a `gofmt` sweep) is `chore`. Using
`style` for formatting buries it in the release notes next to real
presentation work.

**`build` covers CI.** These were once split, and the split did not
hold — workflow edits landed as both `build(ci)` and `ci(...)` in
roughly equal numbers. One type, with the area in the scope:
`build(ci)`, `build(make)`, `build(release)`. `ci` still parses so the
commits already in history stay explicitly skipped; don't use it for
new work.

### The breaking-change bar

A `!` marker or `BREAKING CHANGE:` footer forces a **major** bump.
Once the project is at 1.x this is unconditional: no `cliff.toml`
setting downgrades it, so the commit message is the only control.
Treat the marker as the version decision it is.

Bosun's public surface is its **commands, flags, and config keys** —
not its Go API, which is entirely `internal/` and importable by no one.

Reserve the marker for changes that break a working invocation **with
no diagnostic** — the user gets silence, wrong behavior, or a failure
that doesn't name the cause:

| Change                                          | Marker |
|-------------------------------------------------|--------|
| Command or flag removed                         | Yes    |
| Flag's meaning changes, same name                | Yes    |
| Config key removed; absence silently changes behavior | Yes |
| Config key renamed; old key errors and names the new one | No |
| Default changes, with the new value reported     | No     |
| Output shape or wording changes                  | No     |

A rename that **fails loudly and names the migration** is a warned
migration, not a break. Use `feat` and put the migration in the commit
body — it lands in the changelog without burning a major.

Two majors so far have both been incidental side effects of a marker
rather than deliberate decisions. If a change genuinely warrants a
major, say so in the PR and cut it on purpose.

### Go module path

`go.mod` declares `github.com/nickawilliams/bosun` with no `/vN`
suffix, so Go's semantic import versioning ignores any `v2+` tag —
`@latest` silently resolves to the newest `v1`. Any real major needs
the module path and every internal import updated in the same change.

## Polish-Before-Refactor Discipline

When polish or feature work surfaces an architectural smell that's out
of scope for the current branch, capture the discovery — don't fix it
silently and don't expand scope. Paired action:

1. **At the smell site**, drop a one-line TODO referencing the open
   refactor ticket: `// TODO(arch #NN): <short smell name>`. Keep it
   under 80 chars; the comment is a pointer, not the explanation.
2. **In the refactor ticket**, append a one-line bullet describing the
   discovery. The ticket holds the actual context; the inline TODO is
   how a future reader of the code finds it.

Both, not either. TODOs scatter without aggregate visibility; tickets
are invisible at the point of patching. The pair covers both failure
modes.

**Non-negotiable:** don't *enlarge* a known leak. Patches that inherit
the existing leak shape are fine when flagged with a TODO. Patches
that add new provider-flavored helpers, new hardcoded provider
formats, or new direct provider imports in `internal/cli/` are not —
either route through the relevant interface or pause and discuss.
