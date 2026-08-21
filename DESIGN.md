# Bosun

A CLI tool for automating repeated SDLC tasks across issue trackers, version
control, CI/CD, and notification systems. Named for the ship's officer who
directs the crew and signals state changes.

## Design Goals

- **Generalized**: Not coupled to any specific vendor (Jira, GitHub, Slack).
  Integrations are modular and swappable.
- **Composable**: Each lifecycle transition triggers a configurable set of
  actions against external systems.
- **Multi-repository aware**: A single issue may span work across multiple
  repositories.
- **Concrete-first**: Let real workflow needs drive the design. Abstract where
  it comes for free, refactor toward generality as patterns emerge.

## Decisions

- **State ownership**: The issue tracker is the source of truth for lifecycle
  state. The CLI triggers transitions but does not maintain its own state store.
- **Multi-repository**: Support 1 issue : N repositories. Most common cases
  are 1:1 (80%) and 1:2 (15%, typically frontend + backend). Commands operate
  across all repositories associated with an issue.
- **Configuration**: Global config at `~/.config/bosun/config.yaml`. Project-
  level overrides via `.bosun/config.yaml` (discovered by walking up from CWD,
  like `.git/`).
- **Language**: Go. Cobra + Viper for CLI and config. Charmbracelet libraries
  (lipgloss, bubbletea, etc.) for terminal UI/UX.
- **Lifecycle stages**: Driven by current workflow. Not a generic state machine
  framework — just the stages we actually need, with the integration points
  being the modularity seam.
- **Toolchain**: Follow patterns from diffscribe/shedoc — Makefile with
  project.yaml, goreleaser, git-cliff, LDFLAGS version injection.
- **Repository association**: `repositories:` config is a list of glob
  patterns resolved to directories containing `.git/`. Replaces both
  `repository_root` and explicit repository names with a single flexible
  mechanism. `--repository` flag on `start` filters which glob-matched
  repositories to operate on for a given issue.
- **Workspace management**: Absorbed from standalone `workspace` utility
  into bosun as a subcommand (`bosun workspace {create,add,status,rm}`).
  Manages git worktrees under `<workspace_root>/<branch>/<repository>/`.
  `bosun start` creates a workspace (branches + worktrees) for the resolved
  repositories. `workspace *` commands function independently from lifecycle
  commands.
- **Project root**: Identified by the presence of a `.bosun/` directory.
  Discovered by walking up from CWD. Houses project-level config overrides.
  Not required — bosun works with global config alone.
- **Repository/workspace layout**: `repositories:` globs define where
  repositories are. `workspace_root` in project config sets where workspaces
  are created (defaults to project root). Workspaces are always on when
  `.bosun/` exists.
- **Subcommand structure**: All commands are top-level Cobra subcommands.
  Lifecycle commands (`start`, `review`, `preview`, `cleanup`, etc.) share an
  `--issue`
  flag defined once via a helper function and added to each command that needs
  it. Utility commands (`workspace`, `create`) don't get the flag. No
  dynamic-first-argument routing — verbs come first, issue is a flag.
- **Notification threading**: Looked up via provider API at runtime (e.g.,
  search Slack channel for messages containing the issue number). No local
  state file — avoids sync/invalidation complexity. Can add caching later if
  API lookups become a performance problem.
- **No local state**: All state is queried from providers at runtime. No
  state file to sync, invalidate, or reconcile. `.bosun/` contains config
  only. If API lookups become a bottleneck, add a cache (disposable,
  rebuildable) — not a state store.
- **Idempotent actions**: Per-repository actions (branch creation, PR
  creation) check for existing state via provider APIs before acting. "Assert
  desired state" rather than "perform operation." Manual actions outside bosun
  don't cause conflicts — bosun skips what's already done.
- **Multi-repository fan-out**: Per-repository actions (VCS, Code Host) fan
  out across all repositories with relevant changes. Per-issue actions
  (Notification, Issue Tracker) happen once and aggregate. Repositories with
  no changes on the branch are skipped.
- **Stage validation**: Lifecycle commands check the issue's current status
  before transitioning. Unexpected status (e.g., running `release` when issue
  is still in "Review") warns and requires confirmation rather than proceeding
  blindly.
- **Issue resolution**: `--issue` flag bound to Viper. Resolution chain:
  (1) explicit `--issue` flag, (2) `BOSUN_ISSUE` env var (works with
  direnv), (3) workspace path derivation (extract issue from
  `<workspace_root>/<branch>/` using `branch.pattern` in reverse), (4) git
  branch name derivation (same parser, different input). Error if none
  resolve.

---

## Architecture

### Capability Architecture

Each external system interaction is defined by a **capability interface** with
domain types. Adapters implement these interfaces for specific vendors. The CLI
commands compose calls to capabilities — they don't know or care which vendor
is behind the interface.

```
Capability (interface)       Adapters
──────────────────────       ──────────────────
issue.Tracker                jira.Adapter
  CreateIssue()              (linear — future)
  GetIssue()
  SetStatus()

code.Host                    github.Adapter
  CreatePR()                 (gitlab — future)
  CreateRelease()
  GetPRStatus()

vcs.VCS                      git.Adapter
  CreateBranch()
  GetBranchStatus()

notify.Notifier              slack.Adapter
  Notify()                   (discord — future)
  ReplyToThread()            (email — future)

cicd.CICD                    githubactions.Adapter
  TriggerWorkflow()          (others future)
  GetWorkflowStatus()
```

### Configuration

Two-tier Viper-managed config. Global settings at `~/.config/bosun/config.yaml`,
project-level overrides at `.bosun/config.yaml` (merged on top).

**Global config** (`~/.config/bosun/config.yaml`):

```yaml
# Provider selection
issue_tracker: jira
code_host: github
notification: slack
cicd: github_actions

# Provider-specific settings
jira:
  base_url: https://mycompany.atlassian.net
  email: you@company.com
  # Auth: token from env var BOSUN_JIRA_TOKEN

github:
  # Relies on gh CLI auth or GITHUB_TOKEN

slack:
  # Auth: BOSUN_SLACK_TOKEN

# Branch naming
branch:
  pattern: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"
  categories:
    story: feature
    bug: fix
    task: chore

# PR defaults
pull_request:
  # Optional, and usually best left unset: each repository's PR then
  # targets that repository's own default branch. Setting it pins one
  # base for EVERY repository, which fails in any repo that lacks it.
  # base: main
  title_pattern: "[{{.IssueNumber}}] {{.IssueTitle}}"

# Issue tracker status mapping (your workflow -> provider states)
statuses:
  ready: "Ready"
  in_progress: "In Progress"
  review: "Review"
  preview: "In Preview Env"
  ready_for_release: "Ready for Release"
  done: "Done"
```

**Project config** (`.bosun/config.yaml`):

```yaml
# Issue tracker project key
jira:
  project: PROJ

# Notification channels
slack:
  channel_review: bb-prs
  channel_prerelease: release_coordination

# Repository patterns (globs resolved to directories containing .git/)
repositories:
  - ./*

# Where workspaces are created (default: project root)
workspace_root: _workspaces
```

**CI/CD workflows** (project config, optional — `bosun init` prompts for these):

```yaml
github_actions:
  workflows:
    preview:
      url_template: "https://{{.Name}}.preview.example.com"
      up:
        target: org/repo/.github/workflows/deploy-preview.yml
        inputs:
          services: services-to-deploy
          name: name
      down:
        target: org/repo/.github/workflows/teardown-preview.yml
        inputs:
          name: name
    release:
      target: org/repo/.github/workflows/deploy.yml
      inputs:
        services: services-to-deploy
```

The `preview` stage has paired `up` (deploy) and `down` (teardown) actions
with their own targets and inputs. `url_template` lives at the stage level
since it describes the env both actions reference; `bosun preview` uses it
to probe whether an environment is already running before redeploying. The
`down` block is optional — when configured, `bosun preview` schedules a
teardown of the prior environment if you supply a different `--name`.

**Preview provider** (project config, optional):

```yaml
preview:
  provider: cicd            # or 'ephemeral'
  api:
    base_url: https://ephemeral-ui.example.dev   # ephemeral only
    auth: gh-cli
```

Two adapters implement the preview capability, selected by
`preview.provider`. Unset means `cicd`, so a config written before this key
existed keeps working.

- **`cicd`** dispatches the workflows configured above, then probes
  `url_template` over HTTP to decide whether an env is up. A probe answers
  only "reachable" or "not", so a provision still in flight and an env torn
  down last week look identical, and it cannot enumerate environments at
  all.
- **`ephemeral`** delegates to an HTTP service that fronts the same
  environments. Because it asks rather than probes, it distinguishes
  `active`, `degraded` (naming the services that failed), `deleting`,
  and gone — and it can list the fleet, which is what
  `bosun preview list` reads. It authenticates with the token from
  `gh auth token`; an expired one is reported as a re-auth prompt rather
  than retried.

`creating` is in the taxonomy but not yet reachable by name: the API
reports in-flight provisions with a null name, because the name is
recovered from the setup job's logs and those aren't written yet. So a
provision in flight still reads as absent, the same answer the probe
gives. Attributing one to a name needs a per-run
`GET /api/workflow-status/:runId`.

Both adapters store the env-to-issue binding under the same issue property,
so switching providers doesn't orphan a running environment. `url_template`
stays under the CI/CD group for both: rendering the URL locally is what lets
`bosun preview` show it before the deploy has landed.

### Project Structure

```
bosun/
├── cmd/bosun/
│   └── main.go                      # Entry point
├── internal/
│   ├── cli/                         # Cobra commands
│   │   ├── root.go
│   │   ├── issue.go                 # Shared --issue flag + resolution
│   │   ├── create.go
│   │   ├── start.go
│   │   ├── review.go
│   │   ├── preview.go
│   │   ├── prerelease.go
│   │   ├── release.go
│   │   ├── cleanup.go
│   │   ├── status.go
│   │   └── workspace.go             # workspace {create,add,status,rm}
│   ├── config/                      # Viper config loading
│   │   └── config.go
│   ├── issue/                       # Issue tracking capability
│   │   ├── issue.go                 # Interface + domain types
│   │   └── jira/                    # Jira adapter
│   │       └── jira.go
│   ├── code/                        # Code hosting capability
│   │   ├── code.go                  # Interface + domain types
│   │   └── github/                  # GitHub adapter
│   │       └── github.go
│   ├── vcs/                         # Version control capability
│   │   ├── vcs.go                   # Interface + domain types
│   │   └── git/                     # Git adapter
│   │       └── git.go
│   ├── notify/                      # Notification capability
│   │   ├── notify.go                # Interface + domain types
│   │   └── slack/                   # Slack adapter
│   │       └── slack.go
│   ├── cicd/                        # CI/CD capability
│   │   ├── cicd.go                  # Interface + domain types
│   │   └── githubactions/           # GitHub Actions adapter
│   │       └── githubactions.go
│   ├── preview/                     # Preview environment capability
│   │   ├── preview.go               # Interface + domain types
│   │   ├── binding.go               # Shared env-to-issue registry
│   │   ├── cicd/                    # Workflow-dispatch adapter
│   │   │   └── cicd.go
│   │   └── ephemeral/               # HTTP deployment-API adapter
│   │       └── ephemeral.go
│   ├── services/                    # Provider construction + registries
│   │   └── services.go
│   ├── workspace/                   # Worktree/workspace management
│   │   └── workspace.go
│   └── ui/                          # Charmbracelet UI components
│       └── ...
├── contrib/
│   ├── completions/
│   └── man/
├── tools/
│   └── gen-man/
├── Makefile
├── project.yaml
├── .goreleaser.yaml
├── cliff.toml
└── go.mod
```

---

## Inputs

- **Identifiers**
  - `<issue-number>` — Issue tracker issue ID
- **Derived Variables** (fetched from issue tracker at runtime)
  - `<issue-title>` — Title from issue tracker
  - `<issue-slug>` — Slugified title for branch names
  - `<category>` — Mapped from issue type (e.g., Story -> `feature`,
    Bug -> `fix`)
  - `<repositories>` — Target git repositories (from config or flags)

## Lifecycle Stages

### 0. Create

Transition: -> `Ready`

```
bosun create --title <title> --description <description> --size <size> --type <bug|story>
```

Actions:

1. Issue Tracker: Create issue with given attributes

### 1. Start

Transition: `Ready` -> `In Progress`

```
bosun start --issue <issue> [--repository <path>...]
bosun start                    # issue resolved from env/workspace/branch
```

Actions:

1. VCS: Create branch `<category>/<issue-number>_<issue-slug>` in target
   repository(s). If workspace support is configured, creates worktrees under
   `<workspace_root>/<branch>/<repository>/`; otherwise creates the branch in
   the repository directly.
2. Issue Tracker: Set issue status to `In Progress`

### 2. Review

Transition: `In Progress` -> `Review`

```
bosun review [--issue <issue>]
```

Actions:

1. Code Host: Create pull request(s) — one per repository with changes
   - Base: `main`
   - Head: `<branch-name>`
   - Title: `[<issue-number>] <issue-title>`
   - Description: omitted for MVP (add `--body`/stdin/templates later)
2. Notification: Notify in review channel with PR URL(s) + issue URL
3. Issue Tracker: Set issue status to `Review`

### 3. Preview

Transition: `Review` -> `In Preview Env`

```
bosun preview [--issue <issue>]
bosun preview list [--user <account>]
```

Actions:

1. CI/CD: Trigger ephemeral environment deployment
2. Notification: Reply to review notification with preview URL

`bosun preview list` reads the shared fleet — everyone's environments, with
who deployed each — so an untracked env can be found and adopted. It needs a
provider that can enumerate; the `cicd` adapter reports that it cannot
rather than printing an empty list.

### 4. Prerelease

Transition: `In Preview Env` -> `Ready for Release`

```
bosun prerelease [--issue <issue>] [--bump patch|minor|major]
```

Actions:

1. Code Host: Create release/tag per repository — version derived from latest
   existing tag (default: next patch). `--bump` overrides the increment level,
   applied independently to each repository's version.
2. Notification: Notify in release channel with service name, release URL,
   description
3. Issue Tracker: Set issue status to `Ready for Release`

### 5. Release

Transition: `Ready for Release` -> `Done`

```
bosun release [--issue <issue>] [--migrations-done]
```

Actions:

1. Pre-flight: Confirm database migrations have been requested/completed.
   Interactive prompt unless `--migrations-done` is passed. Skipped for
   repositories that don't require migrations (configurable per repository).
2. CI/CD: Trigger production deployment workflow
3. Issue Tracker: Set issue status to `Done`

### 6. Cleanup

```
bosun cleanup [--issue <issue>]
```

Actions:

1. Workspace: Remove worktrees for all repositories
2. VCS: Delete local and remote feature branches (idempotent — skips
   branches already deleted by code host merge settings)

No lifecycle transition — issue is already `Done`. This is housekeeping.

### 7. Status

```
bosun status [--issue <issue>]
```

Displays:

- Current lifecycle stage (derived from issue tracker status)
- Issue Tracker: Issue details + status
- VCS: Branch status per repository
- Code Host: PR status, review state per repository
- CI/CD: Last build/deploy status
- Ephemeral: Preview environment status + URL

---

## Workspace Management

Subcommand for managing git worktree workspaces. Used internally by
`bosun start`, but also usable directly for worktree operations without
the issue lifecycle.

### Layout

Repositories are discovered via `repositories:` globs. `workspace_root` sets
where workspaces are created (defaults to project root).

```
<project-root>/                         # contains .bosun/
├── .bosun/
│   └── config.yaml                     # repositories: [./*], workspace_root: _workspaces
├── my-service/                         # repositories matched by glob
├── my-frontend/
└── _workspaces/
    └── feature/
        └── PROJ-123_add-widget/        # workspace = branch name
            ├── my-service/             # worktree
            └── my-frontend/            # worktree
```

Uniform structure regardless of repository count. Branch name can include
slashes (creates nested directories). All repositories in a workspace share
the same branch name.

### Commands

```
bosun workspace create [--from-head] <name> <repositories...>
bosun workspace delete [--force] [<name>]
bosun workspace add    [--from-head] [<repositories...>]
bosun workspace rm     [--force] [<repositories...>]
```

The verb pairs split by scope: **`create` / `delete`** operate on the
workspace itself; **`add` / `rm`** operate on the repositories within
an existing workspace.

- **create**: Create a new workspace with worktrees for each repository
  under `<workspace_root>/<name>/`. Branches from each repository's
  default branch by default; `--from-head` branches from current HEAD.
- **delete**: Delete a workspace entirely — remove every worktree, the
  local + remote branch, and the workspace directory. Workspace name is
  a positional shortcut or resolved via the standard chain
  (`--workspace`, `BOSUN_WORKSPACE`, CWD, or interactive picker).
  Refuses if any repository is dirty unless `--force`.
- **add**: Add repositories to an existing workspace. Workspace resolved
  via the standard chain. Prompts for repositories if none given.
  `--from-head` branches from current HEAD.
- **rm**: Remove specific repositories from an existing workspace —
  worktree, local branch, and remote branch — leaving the workspace
  itself intact. Workspace resolved via the standard chain. Prompts to
  multi-select if no repositories are given. Refuses if any named
  repository is dirty unless `--force`.

(Per-repo status across a workspace is available via `bosun status`
when CWD'd inside one.)

### Compatibility with external worktree tools

Git allows multiple worktrees per repository (each on a different branch).
Tools like Claude Code that create ephemeral worktrees for parallel agent
execution are compatible — they create additional worktrees of the same
underlying repository on temporary branch names. The only constraint is git's rule that a branch
can only be checked out in one worktree at a time, which ephemeral tools
handle by using their own branch names.

