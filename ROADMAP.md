# Roadmap

Planned work, deferred refactors, and future ideas.

## In Progress

### CI/CD (Phase 6)

- [x] `cicd.CICD` interface and domain types
- [x] GitHub Actions adapter (workflow dispatch)
- [x] Wire `preview` and `release` commands
- [x] WorkflowSpec config (global string or per-repo map)
- [x] Relative workflow paths (resolved from git remote)
- [x] Init wizard for GitHub Actions setup
- [x] Monorepo service discovery — `services` config supports string, list,
  and map forms. Map form includes per-service path prefixes for
  change-based filtering (diff branch vs default, skip unchanged services).
  Pre-flight push check ensures diff matches CI state.
- [ ] CI build-status-based service detection — query GitHub Actions workflow
  runs to check which services actually have built images (like ephemeral-ui's
  `pr-build-status.ts` approach). More accurate than file-diff for monorepos
  with transitive dependencies. Would use service → workflow path mapping
  from the map-form services config and the existing `cicd.CICD` interface.
- [ ] Glob pattern expansion for workflow paths
- [ ] Workflow inputs and ref override (object form config)

## Planned

### Config Schema Refactor

Separate config resolution logic from UI prompting. Extract a pure function
that takes `ConfigKey` + viper state and returns a resolution action (skip,
use default, prompt with options). The prompt layer just executes the action.

**Why:** Unit tests become trivial (no terminal simulation), new config key
types are schema fields instead of code branches.

**Scope:** `require.go` (resolveGroup, resolveConfigKey), `schema.go`
(ConfigKey), `init.go` (service wizard). The CI/CD custom setup
(`init_cicd.go`) stays as-is since its polymorphic config doesn't fit the
schema model.

**Additional considerations:**
- Schema defaults should be available at runtime via viper (currently only
  applied during resolveGroup, so custom setup code duplicates defaults)
- Support explicitly unsetting a value during init prompts (empty input
  currently means "accept the default" — there's no way to express "leave
  this unset")

### Confirmation Flag Consolidation

Unify `--yes` and `--force` across the CLI. Today they're orthogonal but
overlapping: `--yes` auto-confirms prompts (init, plan, workspace rm),
`--force` overrides safety checks (cleanup, workspace rm). The new `--force`
on `preview` blurs the line by combining "auto-confirm" with "prefer
destructive/replace."

**Why:** Two flags with overlapping semantics is a recipe for "which one do
I need?" confusion. A single flag with a clear mental model is easier to
teach and document.

**Scope:** Pick one canonical name (likely `--force`) and migrate all
commands; keep the other as a deprecated alias for one release. Audit each
call site to confirm the merged semantic ("auto-confirm + override safety")
is correct everywhere or needs separation.

### Status Command — CI/CD Integration

- [ ] Last build/deploy status per repository
- [ ] Preview environment status + URL

### Non-Interactive Output Mode

Full support for raw, machine-readable output across all commands when stdout
is not a TTY. Today only commands annotated `output: "raw"` (e.g. `config get`)
switch to compact mode; `ui.IsTerminal()` exists but is never consulted, so
piped invocations still get styled chrome.

**Scope:**
- Auto-detect non-TTY stdout in `PersistentPreRunE` and force compact display
  + no-color (still overridable by explicit config/flags).
- Audit every command for a sensible raw representation. Example: `config show`
  should emit the resolved config as YAML when non-interactive.
- Consider a `--output {auto,text,yaml,json}` convention so users can opt into
  structured output explicitly even from a TTY.

### Man Pages and Shell Completions

- [ ] Man page generation (`tools/gen-man/`)
- [ ] Shell completions generation (`tools/gen-completions/`)

### Shell Integration

A `bosun shell-init [bash|zsh|fish]` command that prints an `eval`-able shell
function, à la `zoxide` / `direnv` / `nvm`. The function wraps the real binary
and runs `builtin cd` after it exits, so commands can effectively change the
parent shell's working directory.

**Why:** A child process can only `chdir(2)` itself; it can't move the parent
shell. Several planned flows want this — without it, the best we can do is
print a "now run `cd …`" hint.

**Use cases:**
- `bosun switch <workspace>` — fuzzy-pick a workspace (and optionally a repo
  within it) and `cd` there. Replaces hand-typing
  `cd .workspaces/feature/PROJ-123/api`.
- `bosun start` — drop the user into the new worktree on success.
- `bosun workspace rm` — `cd` to project root when the removed workspace
  contained the user's CWD (today we just print a recovery hint and `chdir`
  the bosun process before deletion).

**Scope:**
- New `shell-init` command, one template per supported shell.
- Wire format between binary and wrapper (env var, sentinel stdout line, fd 3
  — pick one; needs to coexist with normal command output).
- Onboarding docs for `eval "$(bosun shell-init zsh)"` in user rc files.

### Issue Picker Improvements

- [ ] Combobox-style picker with server-side search (OptionsFunc or custom
  bubbletea model) replacing the current select + manual-entry two-step

## Future Ideas

- OAuth authentication for Jira (browser-based 3LO flow, refresh token in
  system keychain, abstract auth behind interface)
- Standalone `bosun issues` command for browsing without lifecycle action
- Auto-configure local development environments for affected repositories
- Code coverage checks against minimums
- Local dev orchestration (start backends, point frontends at them)
- LLM-assisted PR description generation (port diffscribe's approach)
- Markdown rendering via glamour for PR body previews and release notes
