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

### Status Command — CI/CD Integration

- [ ] Last build/deploy status per repository
- [ ] Preview environment status + URL

### Man Pages and Shell Completions

- [ ] Man page generation (`tools/gen-man/`)
- [ ] Shell completions generation (`tools/gen-completions/`)

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
