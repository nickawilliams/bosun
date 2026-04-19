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
  with `BOSUN_` prefix override both via Viper.
