# Application Model

How bosun is structured conceptually, independent of UI rendering.
The tree below is the source of truth for "what must render" — every
command is decomposed into Phases and Tasks that map to UI components
in `internal/ui/`.

## Schema

```
Application
+-- Command[]
    +-- Command[]              -- parent commands (e.g. config, workspace)
    +-- Phase[]                -- may be a plain Phase or a Phase.Plan
    |   +-- Task[]             -- may be a plain Task or a Task.Action
    +-- Task[]                 -- loose tasks directly under a Command
```

## Types

- **Command** — an invocable unit. Leaf (does work) or parent
  (delegates to subcommands).
  - **Command.Lifecycle** — SDLC stage. Always multi-step.
  - **Command.Utility** — support / inspection tooling.
  - **Command.Hidden** — not in help (`demo`, `captain`).
- **Phase** — a collection of Tasks around a shared purpose. Run
  sequentially; tasks within a Phase are not gated by confirmation.
  - **Phase.Plan** — assess-confirm-apply lifecycle. Children are
    assessed up front, the user confirms, then apply runs. Children
    must be `Task.Action`.
- **Task** — one logical unit of work. May produce a value, may fail.
  - **Task.Action** — has explicit `Assess` and `Apply` steps with a
    verb (create / modify / destroy / no-change). Only appears as a
    child of `Phase.Plan`.

### Phase.Plan vs direct mutating Task

- **Phase.Plan** when the mutation is composed of multiple discrete
  actions the user should see as a list and confirm together.
- **Direct mutating Task** when the mutation is one logical operation.
  The user gate is elsewhere (`--approve`, `--dry-run`, or an interactive
  confirm before the Task runs).

## Command tree

```
Application: bosun
|
+-- Command.Lifecycle: create                        -- create an issue
|   +-- Phase: gather issue fields
|   |   +-- Task: collect title                      -- --title or interactive prompt
|   |   +-- Task: collect description                -- --description or interactive prompt
|   |   +-- Task: collect type                       -- --type or interactive prompt
|   |   +-- Task: collect size                       -- --size or interactive prompt
|   +-- Phase.Plan: create issue
|   |   +-- Task.Action: create issue                -- tracker API call
|   +-- Task: display result                         -- post-Plan
|
+-- Command.Lifecycle: start                         -- start work on an issue
|   +-- Phase: gather issue context
|   |   +-- Task: identify issue
|   |   +-- Task: fetch issue details                -- best-effort
|   +-- Phase: choose branch name
|   |   +-- Task: resolve slug
|   |   +-- Task: assemble branch name
|   +-- Phase: choose repositories
|   |   +-- Task: load repository set
|   |   +-- Task: select repositories               -- interactive multi-select
|   +-- Phase.Plan: provision local workspace
|       +-- Task.Action: create branch               -- per repository
|       +-- Task.Action: create worktree             -- per repository
|       +-- Task.Action: transition issue status
|
+-- Command.Lifecycle: review                        -- open PRs for review
|   +-- Phase: gather issue context
|   |   +-- Task: identify issue
|   |   +-- Task: fetch issue details
|   +-- Phase: choose repositories
|   |   +-- Task: load active repository set
|   +-- Phase: resolve repository identities
|   |   +-- Task: get current branch                 -- per repository
|   |   +-- Task: parse remote owner/name            -- per repository
|   +-- Phase: collect PR metadata                   -- shared across repos
|   |   +-- Task: resolve base branch                -- per repo default; --base/config overrides all
|   |   +-- Task: resolve PR title
|   |   +-- Task: resolve PR body
|   |   +-- Task: resolve reviewers
|   |   +-- Task: resolve team reviewers
|   |   +-- Task: resolve assignees
|   +-- Phase: customize PR metadata                 -- opt-in, --interactive
|   |   +-- Task: select repositories to customize   -- interactive multi-select, none checked
|   |   +-- Task: edit metadata                      -- per selected repository
|   +-- Phase: ensure branches are pushed
|   |   +-- Task: check unpushed commits             -- per repository
|   |   +-- Task: confirm push                       -- interactive
|   |   +-- Task: push branch                        -- per repository
|   +-- Phase.Plan: open pull requests
|       +-- Task.Action: create or claim PR          -- per repository
|       +-- Task.Action: transition issue status
|       +-- Task.Action: send review notification
|
+-- Command.Lifecycle: preview                       -- deploy to preview env
|   +-- Phase: gather issue context
|   |   +-- Task: identify issue
|   |   +-- Task: fetch issue details
|   +-- Phase: resolve preview environment
|   |   +-- Task: probe stored env name
|   |   +-- Task: probe requested env name
|   |   +-- Task: decide outcome
|   |   +-- Task: prompt on conflict                 -- interactive
|   |   +-- Task: clear stale metadata
|   +-- Phase: gather change set
|   |   +-- Task: detect affected services
|   |   +-- Task: lookup PRs for affected
|   +-- Phase.Plan: deploy preview
|       +-- Task.Action: trigger teardown workflow
|       +-- Task.Action: adopt env in tracker
|       +-- Task.Action: trigger deploy workflow
|       +-- Task.Action: transition issue status
|       +-- Task.Action: send review notification
|
+-- Command.Lifecycle: prerelease                    -- cut release candidates
|   +-- Phase: gather issue and scope
|   |   +-- Task: identify issue
|   |   +-- Task: load active repository set
|   +-- Phase: derive release versions
|   |   +-- Task: get current branch                 -- per repository
|   |   +-- Task: parse remote owner/name            -- per repository
|   |   +-- Task: fetch latest tag                   -- per repository
|   |   +-- Task: compute next version               -- per repository
|   +-- Phase.Plan: cut releases
|       +-- Task.Action: create release              -- per repository
|       +-- Task.Action: transition issue status
|       +-- Task.Action: send release notification
|
+-- Command.Lifecycle: release                       -- trigger production release
|   +-- Phase: gather issue context
|   |   +-- Task: identify issue
|   |   +-- Task: fetch issue details
|   +-- Phase: confirm prerequisites
|   |   +-- Task: confirm migrations done
|   +-- Phase.Plan: deploy to production
|       +-- Task.Action: trigger production workflow
|       +-- Task.Action: transition issue status
|
+-- Command.Lifecycle: cleanup                       -- tear down after merge
|   +-- Phase: gather scope
|   |   +-- Task: identify issue
|   |   +-- Task: load repository set
|   +-- Phase: verify safety
|   |   +-- Task: check dirty state                  -- per repository
|   +-- Phase.Plan: remove workspace
|   |   +-- Task.Action: remove worktree             -- per repository
|   |   +-- Task.Action: delete branch               -- per repository
|   +-- Task: prune empty workspace dir              -- post-Plan
|
+-- Command.Utility: config                          -- inspect / modify config
|   +-- Command: show                                -- render resolved config
|   +-- Command: get                                 -- print value at key
|   +-- Command: set                                 -- write key/value
|   +-- Command: unset                               -- remove key
|   +-- Command: check                               -- validate completeness
|   +-- Command: edit                                -- open in $EDITOR
|
+-- Command.Utility: doctor                          -- diagnose environment
|   +-- Phase: environment
|   +-- Phase: project
|   +-- Phase: integrations
|   +-- Phase: CI/CD
|   +-- Task: render summary
|
+-- Command.Utility: init                            -- initialize project
|   +-- Phase: detect existing state
|   +-- Phase: gather project settings
|   +-- Task: write config
|   +-- Task: display project summary
|   +-- Phase: configure services                    -- per service group
|   +-- Task: display next steps
|
+-- Command.Utility: status                          -- show issue + repo state
|   +-- Phase: issue
|   +-- Phase: repositories
|   +-- Phase: pull requests                         -- conditional
|   +-- Phase: preview environment                   -- conditional
|
+-- Command.Utility: workspace                       -- manage workspaces
|   +-- Command: create
|   +-- Command: add
|   +-- Command: status
|   +-- Command: rm
|
+-- Command.Hidden: demo                             -- UI component reference
+-- Command.Hidden: captain                          -- easter egg
```

## Patterns

1. **Issue-context phase is near-universal in Lifecycle.** Six of
   seven start with identify issue + fetch details. Only `create`
   skips it.

2. **Per-repository fan-out is rampant.** Most Lifecycle commands
   and several Utility commands have per-repository tasks or actions.

3. **Phase.Plan is universal in Lifecycle, absent in Utility.** Every
   Lifecycle command has exactly one Phase.Plan. Utility commands
   mutate via direct Tasks.

4. **Lifecycle ordering follows the SDLC**: create, start, review,
   preview, prerelease, release, cleanup.

## Heading & breadcrumb structure

Every command run opens with a heading whose visible breadcrumb
follows this shape:

```
[data segments] › [command path]
```

That is: location first ("where am I"), then action ("what am I
doing"). Rendered as a single Card.Root by the UI layer (see
`internal/ui/README.md`). The bosun ASCII logo replaces the
implicit "Bosun" root segment.

### Command path

One or more segments describing *what command this is*. Usually
just the command name (or the full subcommand chain); the data
segments below disambiguate mode visually, so the command name
generally stays mode-neutral.

- **Top-level commands**: a single segment. Examples: `Start`,
  `Review`, `Cleanup`, `Doctor`, `Init`.
- **Subcommand commands**: parent + child. Examples:
  `Workspace › Create`, `Config › Show`.
- **Scope-aware commands**: a small number of commands render
  different titles per scope when the scope materially changes
  what's shown. `status` is the canonical example —
  `Project Status` vs `Workspace Status` — because the two
  views surface fundamentally different information. Scope-aware
  titles are registered via `setTitleResolver` in `header.go`.

### Data segments

Zero or more segments after the command path naming the data the
command is operating on. The breadcrumb's *shape* (which data
segments are present, in what order) directly conveys what the
command is doing — no mode qualifier on the command name needed.

The hierarchy is: **project › issue/workspace › repo**.

- **Project** is auto-detected from the current directory. It's
  included for any command that acts within a project context.
- **Issue / workspace** appears when the command is acting on a
  specific issue (via `--issue` or auto-detected from CWD inside
  a workspace). For workspace commands the issue ID is the
  identifier; the workspace name itself lives in the body.
- **Repo** appears when the command's scope narrows to a single
  repository within the project or workspace.

Meta commands (`help`) and tool-info commands run outside any
project context skip project entirely.

#### Status command shape (canonical multi-mode example)

| Mode | Breadcrumb |
| ---- | ---------- |
| Project (no workspace, no specific repo) | `Clearstory › Project Status` |
| Repo (single-repo project, not a workspace) | `Clearstory › extracker › Project Status` |
| Workspace (issue-centric) | `Clearstory › EX-30434 › Workspace Status` |
| Workspace + repo focus | `Clearstory › EX-30434 › extracker › Workspace Status` |
| Outside any project | `Project Status` |

#### Other commands

| Command | Breadcrumb |
| ------- | ---------- |
| `bosun start --issue EX-30434` | `Clearstory › EX-30434 › Start` |
| `bosun review` (in workspace) | `Clearstory › EX-30434 › Review` |
| `bosun cleanup` | `Clearstory › EX-30434 › Cleanup` |
| `bosun preview` | `Clearstory › EX-30434 › Preview` |
| `bosun prerelease` | `Clearstory › EX-30434 › Prerelease` |
| `bosun release` | `Clearstory › EX-30434 › Release` |
| `bosun create` (no issue yet) | `Clearstory › Create` |
| `bosun workspace create EX-30434` | `Clearstory › EX-30434 › Workspace › Create` |
| `bosun workspace add my-api` (in workspace) | `Clearstory › EX-30434 › my-api › Workspace › Add` |
| `bosun workspace rm my-api` (in workspace) | `Clearstory › EX-30434 › my-api › Workspace › Rm` |
| `bosun workspace delete EX-30434` | `Clearstory › EX-30434 › Workspace › Delete` |
| `bosun config show` | `Clearstory › Config › Show` |
| `bosun config check` | `Clearstory › Config › Check` |
| `bosun config set foo.bar baz` | `Clearstory › foo.bar › Config › Set` |
| `bosun config unset foo.bar` | `Clearstory › foo.bar › Config › Unset` |
| `bosun config get foo.bar` | (raw output — no breadcrumb) |
| `bosun init` | `Clearstory › Initialize` |
| `bosun doctor` (in project) | `Clearstory › System Check` |
| `bosun doctor` (outside project) | `System Check` |
| `bosun help` | `Help` |
| `bosun help start` | `Help › Start` |
| `bosun help workspace create` | `Help › Workspace › Create` |

Notes:

- **`workspace status` is removed** — the root `bosun status`
  with workspace mode (`Clearstory › EX-30434 › Workspace Status`)
  covers the same view; having two paths to the same output is
  redundant.
- **Doctor results** render below the heading as normal timeline
  content, not as breadcrumb segments.
- **Config get / set / unset** with a key argument include the
  key as a trailing data segment when the command has a heading.
  `config get` is raw-output and skips the heading entirely.

### Color conventions

| Segment kind | Color |
| ------------ | ----- |
| Command-path segments | bold, `Palette.Primary` (indigo) |
| Separator (`›`) | bold, `Palette.Recessed` |
| Data segments | bold, `Palette.Success` (green) when tinted via `Card.AbsorbedTitleColor` — green is the convention for data identifiers |
| Absorbed state glyph | the absorbed card's state color (✓ success / ✗ error / spinner = primary) — suppressed via `Card.HideAbsorbedGlyph` when the data segment is purely informational |

### Implementation

- Command-path segments come from the `headerAnnotationTitle`
  annotation (see `header.go` and `commandBreadcrumb`). Multi-
  segment subcommand paths embed `›` directly in the annotation,
  e.g. `"Workspace › Create"`. Scope-aware commands register a
  function via `setTitleResolver` instead, returning the title
  segment from the resolved `CommandContext` at header-render time.
- Data segments are contributed by **non-root cards emitted after
  the heading**, via the UI layer's "squish" mechanism. The first
  card's title becomes the next data segment in the breadcrumb;
  the card's body content renders below the box. See
  `internal/ui/squish.go`.
- Cards that contribute a data segment typically opt in to
  `Card.AbsorbedTitleColor(ui.Palette.Success)` and
  `Card.HideAbsorbedGlyph()` — green text, no leading glyph.
- Commands with no data segments emit no absorbed card after the
  heading; the breadcrumb stays as just the command path.

### Examples

```
[Bosun logo box]
[breadcrumb closing line:]
 │  Clearstory › EX-30434 › Start ────────────╯
```

```
 │  Clearstory › EX-30434 › Workspace Status ─╯
```

```
 │  Clearstory › EX-30434 › extracker › Workspace Status ╯
```

```
 │  Clearstory › EX-30434 › Workspace › Create ╯
```

```
 │  System Check ─────────────────────────────╯
```

```
 │  Help › Workspace › Create ────────────────╯
```
