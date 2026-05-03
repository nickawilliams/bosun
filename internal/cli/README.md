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
  The user gate is elsewhere (`--yes`, `--dry-run`, or an interactive
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
|   +-- Phase: collect PR metadata
|   |   +-- Task: resolve base branch
|   |   +-- Task: resolve PR title
|   |   +-- Task: resolve PR body
|   |   +-- Task: resolve reviewers
|   |   +-- Task: resolve team reviewers
|   |   +-- Task: resolve assignees
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
|   +-- Phase: confirm prerequisites
|   |   +-- Task: confirm migrations done
|   +-- Phase: gather issue context
|   |   +-- Task: identify issue
|   |   +-- Task: fetch issue details
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
