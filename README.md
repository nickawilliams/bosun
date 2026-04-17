# Bosun

A CLI tool for automating repeated SDLC tasks across issue trackers, version
control, CI/CD, and notification systems. Named for the ship's officer who
directs the crew and signals state changes.

See [DESIGN.md](DESIGN.md) for the full design document.

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

- [x] `bosun init` with auto-detection of repos and interactive setup
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
- [x] Multi-select repo picker for `start` command
- [x] `RunCardReplace` for in-place card updates (e.g., spinner → result)
- [x] Schema-driven config forms (select, text, secret inputs)
