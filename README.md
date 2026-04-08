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

### Phase 2: VCS + Workspace

- [ ] `vcs.VCS` interface and domain types
- [ ] Git adapter (branch creation, status, deletion)
- [ ] Workspace management (worktree create, add, status, rm)
- [ ] Wire `start` and `cleanup` to real VCS/workspace operations
- [ ] Issue resolution from workspace path and branch name
- [ ] Tests for VCS and workspace operations

### Phase 3: Issue Tracking

- [ ] `issue.Tracker` interface and domain types
- [ ] Jira adapter
- [ ] Wire `create` and lifecycle status transitions
- [ ] Stage validation (check current status before transitioning)
- [ ] Tests for issue tracking

### Phase 4: Code Hosting

- [ ] `code.Host` interface and domain types
- [ ] GitHub adapter (PR creation, release/tag creation)
- [ ] Wire `review` (create PRs) and `prerelease` (create releases)
- [ ] Version derivation from existing tags with `--bump` override
- [ ] Tests for code hosting

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

- [ ] `status` command aggregating all capabilities
- [ ] Charmbracelet UI (spinners, tables, colors)
