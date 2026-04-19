# Bosun

A CLI tool for automating repeated SDLC tasks across issue trackers, version
control, CI/CD, and notification systems. Named for the ship's officer who
directs the crew and signals state changes.

See [DESIGN.md](DESIGN.md) for the full design document.

## Lifecycle Commands

### Timeline

| #   | Actor              | Issue Status            | VCS                          | Code Host       | Env               | Notify          |
| --- | ------------------ | ----------------------- | ---------------------------- | --------------- | ----------------- | --------------- |
| —   | _(backlog)_        | Backlog                 | —                            | —               | —                 | —               |
| 1   | `bosun create`     | **→ Ready**             | —                            | —               | —                 | —               |
| 2   | `bosun start`      | **→ In Progress**       | Branch + worktrees           | —               | —                 | —               |
| —   | _developer works_  | In Progress             | Commits pushed               | —               | —                 | —               |
| 3   | `bosun review`     | **→ Review**            | —                            | PRs opened      | —                 | Review channel  |
| —   | _code review_      | Review                  | —                            | Approvals, CI   | —                 | —               |
| 4   | `bosun preview`    | **→ In Preview Env**    | —                            | —               | Deployed from PRs | Thread w/ URL   |
| —   | _test + iterate_   | In Preview Env          | Fixes pushed                 | PRs updated     | Env updated       | —               |
| —   | _PRs merged_       | In Preview Env          | —                            | PRs merged      | —                 | —               |
| 5   | `bosun prerelease` | **→ Ready for Release** | —                            | Tags + releases | —                 | Release channel |
| 6   | `bosun release`    | **→ Done**              | —                            | —               | Prod deployed     | —               |
| 7   | `bosun cleanup`    | _(Done)_                | Branches + worktrees deleted | —               | —                 | —               |

`bosun status` is not a lifecycle step — it queries all systems and displays
current state at any point.

`preview` only transitions the issue status when the issue is in the expected
source state (`Review`). If the issue is still `In Progress` (e.g., only draft
PRs exist via `review --draft`), the deployment still happens but the status
stays put.

### Progress

- [x] **create** — Create a new issue
  - [x] Issue Tracker: Create issue with given attributes
- [x] **start** — Begin work on an issue
  - [x] VCS: Create branch with naming from issue metadata
  - [x] Workspace: Create worktrees under workspace root
  - [x] Issue Tracker: Transition to `In Progress`
- [ ] **review** — Submit for code review
  - [x] Code Host: Create pull request(s) per repository with changes
  - [ ] Notification: Notify review channel with PR + issue URLs
  - [x] Issue Tracker: Transition to `Review`
- [ ] **preview** — Deploy to preview environment
  - [ ] CI/CD: Trigger ephemeral environment deployment
  - [ ] Notification: Reply to review thread with preview URL
  - [x] Issue Tracker: Transition to `In Preview Env`
- [ ] **prerelease** — Prepare release artifacts
  - [x] Code Host: Create release/tag per repository (version from latest tag + `--bump`)
  - [ ] Notification: Notify release channel with release details
  - [x] Issue Tracker: Transition to `Ready for Release`
- [ ] **release** — Deploy to production
  - [x] Pre-flight: Database migration confirmation
  - [ ] CI/CD: Trigger production deployment workflow
  - [x] Issue Tracker: Transition to `Done`
- [x] **cleanup** — Remove workspace and feature branches
  - [x] Workspace: Remove worktrees for all repositories
  - [x] VCS: Delete local and remote feature branches
- [ ] **status** — Show issue lifecycle status
  - [x] Issue Tracker: Issue details + status
  - [x] VCS: Branch status per repository
  - [x] Code Host: PR status and review state per repository
  - [ ] CI/CD: Last build/deploy status
  - [ ] Ephemeral: Preview environment status + URL

## Implementation Plan

### Phase 1: Skeleton + Config

- [x] Project scaffolding (go.mod, Makefile, project.yaml, goreleaser, cliff.toml)
- [x] Root command with `--version` flag and LDFLAGS injection
- [x] Two-tier Viper config loading (global + `.bosun/` discovery)
- [x] Shared `--issue` flag with `BOSUN_ISSUE` env var binding
- [x] Stub commands for all lifecycle stages and workspace
- [x] Tests for config loading and issue resolution

```sh
bosun --version
bosun --help
bosun start --help
```

### Phase 2: VCS + Workspace

- [x] `--dry-run` persistent flag on root command
- [x] `vcs.VCS` interface and domain types
- [x] Git adapter (branch creation, status, deletion)
- [x] Workspace management (worktree create, add, status, rm)
- [x] Wire `start` and `cleanup` to real VCS/workspace operations
- [x] Issue resolution from workspace path and branch name
- [x] Tests for VCS and workspace operations

```sh
bosun start --issue PROJ-123
bosun workspace create feature/PROJ-123 my-service my-frontend
bosun workspace status feature/PROJ-123
bosun cleanup --issue PROJ-123
bosun start --issue PROJ-123 --dry-run
```

### Phase 3: Issue Tracking

- [x] `issue.Tracker` interface and domain types
- [x] Jira adapter (create, get, transition via REST API v3)
- [x] Wire `create` and lifecycle status transitions
- [x] Stage validation (check current status before transitioning)
- [x] Branch naming from issue metadata (type + slugified title)
- [x] Tests for issue tracking

```sh
bosun create --type story --title "Add widget endpoint" --size medium
bosun start --issue PROJ-123
bosun review --issue PROJ-123 --dry-run
```

### Phase 4: Code Hosting

- [x] `code.Host` interface and domain types
- [x] GitHub adapter (PR creation, release/tag creation, remote URL parsing)
- [x] Auth: gh CLI → GITHUB_TOKEN env → config → JIT prompt
- [x] Wire `review` (create PRs) and `prerelease` (create releases)
- [x] Version derivation from existing tags with `--bump` override
- [x] PR status display in `status` command
- [x] Idempotent PR creation (detects existing PRs)
- [x] Tests for code hosting

```sh
bosun review --issue PROJ-123
bosun prerelease --issue PROJ-123 --bump minor
bosun status --issue PROJ-123
```

### Phase 5: Notifications

- [ ] `notify.Notifier` interface and domain types
- [ ] Slack adapter
- [ ] Wire `review` (notify), `preview` (reply to thread), `prerelease` (release channel)
- [ ] Thread lookup via Slack API
- [ ] Tests for notifications

### Phase 6: CI/CD

- [ ] `cicd.CICD` interface and domain types
- [ ] GitHub Actions adapter
- [ ] Wire `preview` (trigger deploy) and `release` (trigger prod deploy)
- [ ] Tests for CI/CD

### Phase 7: Status + UI

- [x] Charmbracelet UI (lipgloss v2, bubbletea v2, bubbles v2, huh v2)
- [x] Timeline card system with state-driven glyphs
- [x] Animated spinners for async operations
- [x] Styled help output (charmbracelet/fang)
- [x] `status` command with issue details, branch status, PR status
- [ ] Man page generation (`tools/gen-man/`, restore `man` dep in Makefile)
- [ ] Shell completions generation (`tools/gen-completions/`)

```sh
bosun status --issue PROJ-123
bosun doctor
bosun --help
```

### Additional: Project Setup + Config

- [x] `bosun init` with auto-detection of repositories and interactive setup
- [x] `bosun config` subcommand (get, set, list, edit, path, show, check, unset)
- [x] `bosun doctor` for config and connectivity health checks
- [x] Centralized config schema with labels, options, defaults, secrets
- [x] JIT config prompting (missing values prompted and saved on first use)
- [x] `XDG_CONFIG_HOME` support, `~/.config/bosun/` default

```sh
bosun init
bosun config set jira.base_url https://mycompany.atlassian.net
bosun config list
bosun config path
bosun doctor
```

### Additional: Plan/Confirm/Apply

- [x] Terraform-style plan/confirm/apply pattern for all lifecycle commands
- [x] Diff-style plan indicators: `+` create, `~` modify, `-` destroy, `=` unchanged
- [x] Stateful PlanCard with 6 states (proposed/applying/success/partial/failure/cancelled)
- [x] `--yes`/`-y` flag to skip confirmation
- [x] Idempotency detection (existing PRs shown as `=` in plan)
- [x] Status transition awareness (current → target shown in plan)
- [x] Tense-aware summaries (future for proposed, past for success)
- [x] Consistent ctrl+c handling (exit 0, "User cancelled" card)

```sh
bosun start --issue PROJ-123         # shows plan, prompts Apply/Cancel
bosun start --issue PROJ-123 --yes   # skips confirmation
bosun start --issue PROJ-123 --dry-run  # shows plan, exits
```

### Additional: UI Polish

- [x] Display modes (compact, comfy) via `display_mode` config
- [x] Color modes (truecolor, ansi, none) via `color_mode` config + `NO_COLOR` support
- [x] Breadcrumb titles on command headers
- [x] Multi-select repository picker for `start` command
- [x] `RunCardReplace` for in-place card updates (e.g., spinner → result)
- [x] Schema-driven config forms (select, text, secret inputs)

### Additional: Issue Picker

- [ ] `ListAssignedIssues()` on `issue.Tracker` interface (Jira: `assignee = currentUser()` JQL)
- [ ] Interactive issue picker in `resolveIssue()` fallback (replaces free-text prompt)
- [ ] Free-text escape hatch for unassigned issues
