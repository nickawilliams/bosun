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

### Confirmation Flag Consolidation — RESOLVED (split, not merged)

Resolved on the `26-refine-command-output` remediation pass, in the
opposite direction the original scope guessed: the two consents are
genuinely different questions, so they got distinct flags instead of
one merged one.

- `--approve` / `-a` (persistent; renamed from `--yes`) answers the
  plan confirmation — "apply this plan". The plan confirm button says
  Approve to match.
- `--force` (per-command) bypasses safety checks only — dirty trees,
  unpushed work, readiness blockers. It no longer implies approval:
  a forced destructive run still confirms its plan (or passes
  `--approve` explicitly).

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

### Help Output in Shared UI Language

`--help` and `bosun help <command>` currently render through fang's default help
template, palette-mapped via `FangColorScheme` (`internal/cli/help.go`) but
laid out in fang's own grammar — a different visual program from `bosun status`
or any other interactive command. The header logo box, breadcrumb, timeline
spine, glyphs, and card vocabulary all stay behind for help.

**Why:** Visual consistency across the whole CLI. A user typing `bosun --help`
should see the same chrome as `bosun status` — same logo, same breadcrumb, same
spine, same card patterns. Today they read as two different programs that
happen to share a color palette.

**Scope:**
- [ ] Custom `cmd.SetHelpFunc` on root (cascades to subcommands); takes
  precedence over fang's auto-generated help.
- [ ] Breadcrumb routing: `Bosun › Help` for root, `Bosun › Help › Status` for
  subcommands, via `headerAnnotationTitle` or a synthetic title resolver.
- [ ] Section renderers mapped onto bosun's card vocabulary:
  description / usage / examples / commands / flags / footer, with one `Item`
  row per subcommand or flag (keyword-styled name + muted description), columns
  aligned via existing `Card.AlignWidth`.
- [ ] Raw-mode fallback (`ui.IsRaw()`) returns plain text — help under `--raw`
  must stay greppable.
- [ ] Preserve fang's other surfaces (error handling, version output,
  manpage-disable). Trim `FangColorScheme` to just the colors still consumed.

**Decisions to make before starting:** width strategy (logo-box width vs full
terminal); glyph for command/flag rows (`▸` is the natural pick); how to handle
hidden commands (keep them hidden, same as today); whether to inline the short
description into the header subtitle or give it its own card.

**Estimate:** 1–2 days of focused work. Not on the path of anything functional
— polish item, defer until the lifecycle commands themselves stabilize.

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

### Dialog / Prompt Primitive

A prompt primitive that owns both the heading card and the form together,
generalizing today's `Dialog` (which only wraps a binary `huh.Confirm`).

**Why:** Two recurring gaps. (1) `huh.Confirm` caps at two buttons, so 3+-button
prompts aren't expressible — e.g. the preview adopt-conflict prompt wants
`[Adopt] [New Name] [Cancel]` but currently ships as a binary `[Adopt] [New
Name]` with ctrl+c standing in for Cancel. (2) The single-input timeline rule
(when a form has one input, the whole card spine reads accent, not just the
focused field) is a manual convention: hand-rolled `slot.Show(card)` + `runForm`
prompts must remember `Card.AccentBody()`, and silently deviate when they don't
(the preview adopt card regressed this until fixed). The card can't infer it
because it doesn't know whether a single- or multi-input form follows.

**Scope:**
- [ ] N-button selector field (horizontal chips using the theme's button styles,
  left/right + tab + enter, esc/ctrl+c cancel) — or a custom bubbletea model —
  since huh has no native 3+-button or horizontal-button widget.
- [ ] Primitive owns the card + form; applies `AccentBody()` automatically for
  single-input forms and leaves it off for multi-input, so the timeline rule
  can't be violated by hand-rolling.
- [ ] Codify button ordering for consistency. Today order is convention only:
  `huh.Confirm` renders affirmative-left / negative-right, and callers happen to
  assign the dismiss action (Cancel/Stop/No) to negative so it lands rightmost —
  but nothing enforces it, and a caller could put Cancel on the left. Decide and
  enforce a rule (e.g. a dedicated dismiss slot that always renders last) so
  proceed/dismiss positions are stable across every prompt.
- [ ] Fold the existing binary `Dialog` into it, and migrate hand-rolled prompts
  (preview adopt, typeahead helpers) onto it.

## Future Ideas

- OAuth authentication for Jira (browser-based 3LO flow, refresh token in
  system keychain, abstract auth behind interface)
- Standalone `bosun issues` command for browsing without lifecycle action
- Auto-configure local development environments for affected repositories
- Code coverage checks against minimums
- Local dev orchestration (start backends, point frontends at them)
- LLM-assisted PR description generation (port diffscribe's approach)
- Markdown rendering via glamour for PR body previews and release notes
