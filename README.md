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
# Version with LDFLAGS injection
bosun --version

# Stub lifecycle commands (print what they would do)
bosun start --issue PROJ-123
bosun review -i PROJ-123
BOSUN_ISSUE=PROJ-123 bosun status

# Stub workspace commands
bosun workspace create feature/PROJ-123 api web
bosun workspace status
bosun workspace rm

# Help and command discovery
bosun --help
bosun start --help
bosun workspace --help
```

### Phase 2: VCS + Workspace

- [ ] `--dry-run` persistent flag on root command
- [ ] `vcs.VCS` interface and domain types
- [ ] Git adapter (branch creation, status, deletion)
- [ ] Workspace management (worktree create, add, status, rm)
- [ ] Wire `start` and `cleanup` to real VCS/workspace operations
- [ ] Issue resolution from workspace path and branch name
- [ ] Tests for VCS and workspace operations

```sh
# Create a workspace with worktrees (requires repos configured in .bosun/)
bosun start --issue PROJ-123
ls _workspaces/feature/PROJ-123_*/

# Or use workspace commands directly (no issue tracker needed)
bosun workspace create feature/PROJ-123 my-service my-frontend
bosun workspace status feature/PROJ-123
bosun workspace add feature/PROJ-123 another-repo

# Issue auto-detection from workspace path
cd _workspaces/feature/PROJ-123_add-widget/my-service
bosun status  # resolves PROJ-123 automatically

# Issue auto-detection from branch name
cd ~/Projects/my-service && git checkout feature/PROJ-123_add-widget
bosun status  # resolves PROJ-123 from branch

# Clean up
bosun cleanup --issue PROJ-123
bosun workspace rm feature/PROJ-123

# Dry run (shows what would happen without creating branches/worktrees)
bosun start --issue PROJ-123 --dry-run
bosun workspace create feature/PROJ-123 api web --dry-run
```

### Phase 3: Issue Tracking

- [ ] `issue.Tracker` interface and domain types
- [ ] Jira adapter
- [ ] Wire `create` and lifecycle status transitions
- [ ] Stage validation (check current status before transitioning)
- [ ] Tests for issue tracking

```sh
# Create an issue in Jira
bosun create --type story --title "Add widget endpoint" --size medium

# Start work (creates branch + transitions Jira to In Progress)
bosun start --issue PROJ-123

# Stage validation (warns if issue is in unexpected state)
bosun review --issue PROJ-123  # warns if not "In Progress"

# Dry run to verify Jira integration without mutating
bosun start --issue PROJ-123 --dry-run
bosun review --issue PROJ-123 --dry-run
```

### Phase 4: Code Hosting

- [ ] `code.Host` interface and domain types
- [ ] GitHub adapter (PR creation, release/tag creation)
- [ ] Wire `review` (create PRs) and `prerelease` (create releases)
- [ ] Version derivation from existing tags with `--bump` override
- [ ] Tests for code hosting

```sh
# Create PRs across all repos with changes
bosun review --issue PROJ-123

# Prepare release (derives next version per repo from tags)
bosun prerelease --issue PROJ-123
bosun prerelease --issue PROJ-123 --bump minor

# Dry run to see what PRs/releases would be created
bosun review --issue PROJ-123 --dry-run
bosun prerelease --issue PROJ-123 --dry-run
```

### Phase 5: Notifications

- [ ] `notify.Notifier` interface and domain types
- [ ] Slack adapter
- [ ] Wire `review` (notify), `preview` (reply to thread), `prerelease` (release channel)
- [ ] Thread lookup via Slack API
- [ ] Tests for notifications

```sh
# Review now also sends Slack notification
bosun review --issue PROJ-123

# Preview replies to the existing Slack thread
bosun preview --issue PROJ-123

# Dry run to see what messages would be sent
bosun review --issue PROJ-123 --dry-run
bosun preview --issue PROJ-123 --dry-run
```

### Phase 6: CI/CD

- [ ] `cicd.CICD` interface and domain types
- [ ] GitHub Actions adapter
- [ ] Wire `preview` (trigger deploy) and `release` (trigger prod deploy)
- [ ] Tests for CI/CD

```sh
# Preview triggers ephemeral deployment
bosun preview --issue PROJ-123

# Release with migration confirmation
bosun release --issue PROJ-123
bosun release --issue PROJ-123 --migrations-done

# Dry run to see what workflows would be triggered
bosun release --issue PROJ-123 --dry-run
```

### Phase 7: Status + UI

- [ ] `status` command aggregating all capabilities
- [ ] Charmbracelet UI (spinners, tables, colors)

```sh
# Full status across all providers
bosun status --issue PROJ-123
```
