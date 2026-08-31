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
  like `.git/`). Repository-level overrides via a committed `.bosun.yaml` in
  each repository, for the handful of keys that describe one repository.
- **Language**: Go. Cobra + Viper for CLI and config. Charmbracelet libraries
  (lipgloss, bubbletea, etc.) for terminal UI/UX.
- **Lifecycle stages**: Driven by current workflow. Not a generic state machine
  framework — just the stages we actually need, with the integration points
  being the modularity seam.
- **Toolchain**: Follow patterns from diffscribe/shedoc — Makefile with
  project.yaml, goreleaser, git-cliff, LDFLAGS version injection.
- **Repository association**: `workspace.repositories` is a list of glob
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
- **Repository/workspace layout**: `workspace.repositories` globs define
  where repositories are. `workspace.root` sets where workspaces are created
  (defaults to project root). Workspaces are always on when
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
- **Issue resolution**: `--issue` is a plain flag, not bound to Viper.
  Resolution chain: (1) explicit `--issue` flag, (2) `BOSUN_ISSUE` env var
  (works with direnv), read from the environment directly so a config file
  can't pin it, (3) workspace path derivation (extract issue from
  `<workspace.root>/<branch>/` using `vcs.branch.template` in reverse),
  (4) git branch name derivation (same parser, different input). Error if
  none resolve.

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

Three-tier Viper-managed config, outermost first:

| Layer     | File                                | Holds                                          |
| --------- | ----------------------------------- | ---------------------------------------------- |
| `global`  | `~/.config/bosun/config.yaml`       | providers, credentials, personal preferences    |
| `project` | `<project>/.bosun/config.yaml`      | where the repositories are, workspace-wide policy |
| `repo`    | `<repository>/.bosun.yaml`          | what one repository is and how it wants its PRs |

Most specific wins, for lists as well as scalars. A repository's
`reviewers: [bob]` **replaces** the project's `reviewers: [alice]` rather
than appending to it — appending would mean a workspace-wide list kept
applying to every repository no matter what any repository said, which is
the behaviour the repo layer exists to end. `reviewers: []` is how a
repository opts out entirely.

Every repository must keep working without a descriptor, so the central
layers are a permanent fallback rather than a migration shim.

A **file**, not a `.bosun/` directory: `FindProjectRoot` walks up looking
for `.bosun/` and returns the first hit, so a repository carrying its
config in a directory of that name would shadow the workspace's project
root for every command run inside it.

Descriptors are read from the **worktree**, not the main checkout, wherever
a command is workspace-scoped. That is what makes service topology and PR
policy branch-scoped: a branch that adds a service, or moves one between
repositories, changes what "affected" means for that branch. A central map
structurally cannot express that.

#### Scope

Each `ConfigKey` declares a `Scope` — the set of layers allowed to set it.
The zero value is `global | project`, so a key reaches a repository's own
`.bosun.yaml` only by asking to. That default is what keeps credentials out
of shared repositories without a rule of its own: a `Secret` key has no
reason to name `repo`, and the default denies it.

Only these keys are repo-scoped today:

- `pull_request.{base, reviewers, team_reviewers, assignees}`
- `services`
- `cicd.workflows.release.target`

Violations ride on the unknown-key check: `bosun config check` walks each
config **file** (not the merged view — merging is precisely the operation
that forgets which file a value came from) and reports keys sitting in a
layer that may not hold them. It reaches every repository it can resolve,
and stays quiet about descriptors when it cannot resolve the repositories
at all.

Two keys hold **different shapes** in the two layers, because centrally they
are keyed by repository name and a descriptor already knows which repository
it is:

| Key                             | Central                             | Descriptor            |
| ------------------------------- | ----------------------------------- | --------------------- |
| `services`                      | `services.<repo>: <value>`          | `services: <value>`   |
| `cicd.workflows.release.target` | `target.<repo>: <value>`            | `target: <value>`     |

Dropping the repository level is not cosmetic. Keeping it would let a
repository configure a *different* repository by naming it — authority a
committed file must not have. `repoKeyed` reads the bare key from a
descriptor and never the nested one, so a descriptor that spells the
central form is inert: it cannot reach another repository's resolution.

The scope check deliberately stops at the path itself for these two
keys. Below it, the two forms are textually identical —
`services.billing` is a repository name centrally and a service name in
a descriptor, with nothing in the text to tell them apart — so "is this
in a layer allowed to hold it?" has no decidable answer there, and
answering anyway would false-positive on the map form that carries
per-service path filtering. What stays decidable, and is still checked,
is the bare path: a lone `services` written centrally names no
repository and configures nothing.

Environment variables name a key as `BOSUN_` + the key path uppercased with
dots turned into underscores, so `BOSUN_ISSUE_TRACKER_TOKEN` addresses
`issue_tracker.token`. **This works for schema-mediated reads, not for every
key.** Viper is configured with `AutomaticEnv` and no key replacer, so it
never maps a dotted key to that name on its own; the mapping is bosun's,
applied by `effectiveEnvValue` — which is what `config check`, `config show`,
`bosun init` and every provider `Require` go through. A key read with a bare
`viper.GetString` (`vcs.branch.template`, `ui.color`, `workspace.root`) does
not see it, and `config show` will report the env value as live even though
nothing reads it. Treat the env layer as reliable for credentials and provider
selection, and as not yet universal for the rest.

The schema is organized on one axis: **every top-level block is a
capability**, and a block earns root level only if that capability exists in
code, registered or not. That is why `preview` is a root block (an interface
with two adapters) and `release` is not (there is no `internal/release`, and a
release deploy is a CI/CD workflow dispatch — so its keys live under `cicd`).
`bosun config check` validates the merged result against this schema, and
reports any key the schema doesn't recognize.

Three keys are deliberately absent from the schema and readable **only** from
the environment: `BOSUN_ISSUE`, `BOSUN_PROJECT`, `BOSUN_WORKSPACE`. They are
per-invocation alternatives to `--issue` / `--project` / `--workspace`, and a
config file that pinned one would pin every command in the project to it.

**Global config** (`~/.config/bosun/config.yaml`) — providers, credentials,
and anything that isn't specific to one project:

```yaml
issue_tracker:
  provider: jira
  base_url: https://mycompany.atlassian.net
  email: you@company.com
  # Auth: token from BOSUN_ISSUE_TRACKER_TOKEN
  statuses:                     # your workflow -> the tracker's own states
    ready: "Ready"
    in_progress: "In Progress"
    review: "Review"
    preview: "In Preview Env"
    ready_for_release: "Ready for Release"
    done: "Done"

code_host:
  provider: github
  # Auth: gh CLI, or BOSUN_CODE_HOST_TOKEN
  merge_method: squash
  pr:
    title_template: "[{{.Issue.Key}}] {{.Issue.Title}}"

notification:
  provider: slack
  # Auth: BOSUN_NOTIFICATION_TOKEN

vcs:
  branch:
    template: "{{.Issue.Key}}-{{.Issue.Slug}}"
    categories:                 # keyed by the tracker's issue types,
      story: feature            # used by templates that reference
      bug: fix                  # {{.Category}}
      task: chore

ui:
  color: truecolor              # truecolor | ansi | none
  compact_header: false
```

The branch template also names the workspace directory — one string, so the
user-entered slug stays recoverable from the branch on resume.

**Project config** (`.bosun/config.yaml`) — where the repositories are, and
which tracker project and channels this one uses:

```yaml
workspace:
  # Globs resolved to directories containing .git/
  repositories:
    - ./*
  # Where workspaces are created (default: project root)
  root: _workspaces

issue_tracker:
  project: PROJ

notification:
  channels:                     # keyed by notification TYPE, not stage
    review: bb-prs
    prerelease: release_coordination
```

**Preview environments** (project config, optional):

```yaml
preview:
  provider: cicd                # or 'ephemeral'
  url_template: "https://{{.Preview.Name}}.preview.example.com"

  # cicd adapter: the workflows to dispatch
  up:
    workflow: org/repo/.github/workflows/deploy-preview.yml
    inputs:
      name: name
      issue: issue-key
  down:
    workflow: org/repo/.github/workflows/teardown-preview.yml
    inputs:
      name: name

  # ephemeral adapter
  base_url: https://ephemeral-ui.example.dev
  auth: gh-cli
```

`preview` has paired `up` (deploy) and `down` (teardown) sub-stages with their
own workflow and inputs. `url_template` sits at the block level because it
describes the env both sub-stages reference; `bosun preview` uses it to probe
whether an environment is already running before redeploying. The `down` block
is optional — when configured, `bosun preview` schedules a teardown of the
prior environment if you supply a different `--name`.

There is no "deploy only these services" input. A subset deploy leaves the
environment half-built, so bosun always deploys the whole set and pins
per-service image tags through `image-overrides` instead.

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
is shared by both: rendering the URL locally is what lets `bosun preview` show
it before the deploy has landed.

**Release workflows** (project config, optional) — under `cicd`, because a
release deploy is a workflow dispatch:

```yaml
cicd:
  provider: github_actions
  workflows:
    release:
      # A workflow path, or a per-repo map. A repo's value may itself be a
      # per-service map, each entry a workflow path or a
      # {workflow, environment} pair; environment defaults to
      # "<service>-production". A repository can set its own value in
      # `.bosun.yaml` with the repo level dropped; the bare
      # whole-workspace scalar form stays central-only.
      target:
        my-service: .github/workflows/deploy.yml
        my-monorepo:
          api:
            workflow: .github/workflows/deploy-api.yml
            environment: api-prod
      inputs:
        version: version
```

**Repo-scoped policy** — reviewers, assignees, and the PR base. Settable
centrally for repositories with no descriptor, and overridable by any
repository that has one:

```yaml
# project config: the default for every repository
pull_request:
  # Unset means "each repository's own default branch", which is right far
  # more often than one workspace-wide literal.
  # base: main
  reviewers: [alice]
  team_reviewers: [backend]
```

```yaml
# <repository>/.bosun.yaml: this repository only
pull_request:
  base: release/2.0
  reviewers: [bob]         # REPLACES [alice]; use [] to request nobody
```

`--reviewer` still *adds* to whatever each repository resolved, while an
answered interactive prompt *pins* one list across the workspace — the
prompt is one question over one candidate list, so its answer can only mean
the latter. The per-repo customization pass is where a repository diverges
from a pinned answer.

**Per-repository services** — which services a repository contributes to the
deploy surfaces:

```yaml
# project config: keyed by repository name
services:
  my-service: my-service           # or a list, or a map of service -> paths
```

```yaml
# <repository>/.bosun.yaml: the repository level dropped
services:
  billing: [billing/]
  search: [search/]
  _shared: [go.mod]                # a match here affects every service
```

The names and the path prefixes always come from the same layer, so a
descriptor that redefines the services is never narrowed by the central
map's prefixes.

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

Repositories are discovered via `workspace.repositories` globs.
`workspace.root` sets where workspaces are created (defaults to project
root).

```
<project-root>/                         # contains .bosun/
├── .bosun/
│   └── config.yaml                     # workspace: {repositories: [./*], root: _workspaces}
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

