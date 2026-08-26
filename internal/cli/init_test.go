package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
	"gopkg.in/yaml.v3"
)

// TestInit covers `bosun init` end-to-end: flag- and prompt-driven
// project settings, the fresh-vs-reinit split, the optional service
// wizard, and the two ways a run ends without writing anything.
//
// Three things about this command shape the scenarios below.
//
// First, init resolves where it WRITES from os.Getwd() rather than
// from --project: it creates `.bosun/` under the working directory.
// It does not ignore --project, though — the flag is registered
// (addProjectFlag) and the nested-project guard reads it, because
// config.FindProjectRoot() returns config.ProjectRootOverride when
// one is set and only walks up from the CWD when one isn't. So every
// scenario t.Chdir's into the directory it means to initialize, and
// fresh-project scenarios call Uninitialize first (NewWorkspace
// creates .bosun/ for every other command's benefit, which init would
// read as a reinit — and which is also what makes Harness.Run inject
// --project). The nested-project scenario has to stay uninitialized
// for exactly that reason: with --project injected the guard fires on
// the override and the upward walk is never exercised.
//
// Second, the harness counts as interactive (ui.Interactive is true
// for any injected reader), so the optional-service wizard runs on
// every non-quick path — one provider gate per integration group, each
// needing a keystroke even when the scenario is about something else.
// Skipping a gate is a bare "\r" on its select; see
// integrationGateCount.
//
// Third, assertions parse the written YAML rather than matching
// substrings: a fresh config carries a commented-out `# provider:
// jira` template, so `strings.Contains(cfg, "provider")` is true for
// a config that configured nothing.
func TestInit(t *testing.T) {
	t.Run("fresh_project/writes_config_with_minimum_fields", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")     // repository patterns
		h.Type("worktrees\r") // workspace root
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		// A minimum config is exactly the two project settings — the
		// integration groups the wizard offered are all declined, and
		// the template's commented-out examples must not materialize
		// as real keys.
		if got := topLevelKeys(cfg); !reflect.DeepEqual(got, []string{"workspace"}) {
			t.Errorf("top-level keys = %v, want [workspace]", got)
		}
		assertSaved(t, h, "Repository Patterns", "src/*")
		assertSaved(t, h, "Workspace Root", "worktrees")
		// The write confirmation is a plain ui.SuccessLine, not a card
		// or a Reporter call — it goes straight to os.Stdout, which the
		// harness captures. It's the only thing telling the user where
		// the project settings landed, so the PATH is the part worth
		// pinning: a confirmation naming a file init didn't write sends
		// the user off to edit the wrong one. See the harness README's
		// note on which output does reach h.Stdout().
		wrote := "Wrote Project Settings to " + displayPath(h.Workspace.ConfigPath())
		if out := ansi.Strip(h.Stdout()); !strings.Contains(out, wrote) {
			t.Errorf("no %q line on stdout; got:\n%s", wrote, out)
		}
	})

	t.Run("fresh_project/detects_repos_from_cwd", func(t *testing.T) {
		h := newInitHarness(t)
		addChildRepo(t, h, "api")
		addChildRepo(t, h, "web")
		// A hidden sibling that is also a repository. Tool caches
		// (.cache, .config, a vendored .git-managed dir) are the common
		// real case, and they are not the user's projects — detection
		// skips dot-directories, and the assertion below is what says
		// so.
		addChildRepo(t, h, ".cache")

		h.Type("\r") // repository patterns — accept the detected default
		h.Type("\r") // workspace root — accept the default
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Detection reports what it found by name, and turns "there
		// are child repositories" into the ./* glob — the workspace
		// root itself is not a repository, so "." is absent.
		ev, ok := h.Reporter.Find("detected repositories")
		if !ok {
			t.Fatalf("no detected-repositories step; reported:\n%s", h.Reporter.Dump())
		}
		if !reflect.DeepEqual(ev.Items, []string{"api", "web"}) {
			t.Errorf("detected = %v, want [api web]", ev.Items)
		}
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"./*"}) {
			t.Errorf("repositories = %v, want [./*]", got)
		}
		assertSaved(t, h, "Repository Patterns", "./*")
	})

	t.Run("fresh_project/detects_cwd_itself_as_a_repository", func(t *testing.T) {
		h := newInitHarness(t)
		// The directory being initialized is itself a repository, with
		// no child repositories — the single-repo project layout, and
		// the only shape that produces the "." glob.
		testharness.Git(t, h.Workspace.Dir, "init")

		h.Type("\r") // repository patterns — accept the detected default
		h.Type("\r") // workspace root — accept the default
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		ev, ok := h.Reporter.Find("detected repositories")
		if !ok {
			t.Fatalf("no detected-repositories step; reported:\n%s", h.Reporter.Dump())
		}
		want := []string{filepath.Base(h.Workspace.Dir) + " (root)"}
		if !reflect.DeepEqual(ev.Items, want) {
			t.Errorf("detected = %v, want %v", ev.Items, want)
		}
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"."}) {
			t.Errorf("repositories = %v, want [.]", got)
		}
	})

	t.Run("fresh_project/no_detect_flag_skips_auto_discovery", func(t *testing.T) {
		h := newInitHarness(t)
		// Same fixture as the detection scenario — repositories are
		// there to be found, and the flag is the only difference.
		addChildRepo(t, h, "api")
		addChildRepo(t, h, "web")

		h.Type("\r") // repository patterns — accept the undetected default
		h.Type("\r") // workspace root — accept the default
		skipIntegrationGates(h)

		if err := h.Run("init", "--no-detect"); err != nil {
			t.Fatalf("init: %v", err)
		}

		if ev, ok := h.Reporter.Find("detected repositories"); ok {
			t.Errorf("scanned despite --no-detect: %v", ev.Items)
		}
		// Without detection the prompt falls back to the generic
		// both-shapes default rather than the ./*-only glob the same
		// fixture produces when scanning is on.
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{".", "./*"}) {
			t.Errorf("repositories = %v, want [. ./*]", got)
		}
	})

	t.Run("repository_globs/from_flag", func(t *testing.T) {
		h := newInitHarness(t)
		// A repository on disk the flag must win over: detection still
		// runs, but a flag-supplied glob means no prompt and no
		// detected fallback.
		addChildRepo(t, h, "api")

		h.Type("worktrees\r") // workspace root — still prompted
		skipIntegrationGates(h)

		if err := h.Run("init", "--repositories", "src/*,vendor/**"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		want := []string{"src/*", "vendor/**"}
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, want) {
			t.Errorf("repositories = %v, want %v", got, want)
		}
		// The flag path never prompts, so it never emits the field's
		// Saved record — that's how the two paths are told apart.
		if _, ok := h.Reporter.Find("Repository Patterns"); ok {
			t.Errorf("prompted for repository patterns despite --repositories:\n%s", h.Reporter.Dump())
		}
	})

	t.Run("repository_globs/from_interactive_prompt", func(t *testing.T) {
		h := newInitHarness(t)

		// Comma-separated with sloppy spacing: the command splits and
		// trims, and the whole line is what gets echoed back.
		h.Type(" src/* ,  vendor/** \r")
		h.Type("worktrees\r")
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		want := []string{"src/*", "vendor/**"}
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, want) {
			t.Errorf("repositories = %v, want %v", got, want)
		}
		assertSaved(t, h, "Repository Patterns", " src/* ,  vendor/** ")
	})

	t.Run("repository_globs/blank_entry_falls_through_to_a_second_prompt", func(t *testing.T) {
		h := newInitHarness(t)

		// Separators only: the entry is non-blank (so it isn't the
		// placeholder default) but every field trims to nothing,
		// leaving the command with no patterns. It asks once more
		// before writing a config without any.
		h.Type(" , \r")        // repository patterns
		h.Type("worktrees\r")  // workspace root
		h.Type("fallback/*\r") // second chance at patterns
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"fallback/*"}) {
			t.Errorf("repositories = %v, want [fallback/*]", got)
		}
		// The first entry was consumed and discarded, not carried
		// through as an empty-string glob.
		if _, ok := h.Reporter.Find("no repositories configured — add patterns to " +
			h.Workspace.ConfigPath()); ok {
			t.Errorf("reported no repositories despite the retry:\n%s", h.Reporter.Dump())
		}
	})

	t.Run("repository_globs/blank_entry_falls_back_to_detected", func(t *testing.T) {
		h := newInitHarness(t)
		addChildRepo(t, h, "api")

		// Same unusable entry, but this time detection has an answer.
		// It wins over re-asking: the second prompt exists for the case
		// where nothing was found at all.
		h.Type(" , \r")       // repository patterns
		h.Type("worktrees\r") // workspace root
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"./*"}) {
			t.Errorf("repositories = %v, want [./*]", got)
		}
		// The entry really was the unusable one — not the detected
		// default arriving by way of an accepted placeholder.
		assertSaved(t, h, "Repository Patterns", " , ")
	})

	t.Run("repository_globs/blank_entry_leaves_them_unconfigured", func(t *testing.T) {
		h := newInitHarness(t)

		// As above, but the second chance is declined too — the give-up
		// path, where init writes its commented-out placeholder rather
		// than a pattern list.
		h.Type(" , \r")       // repository patterns
		h.Type("worktrees\r") // workspace root
		h.Type("\r")          // second chance at patterns — declined
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		// The config is still written, with the repositories key left
		// as an empty placeholder, and the user is told what's missing
		// and where to add it.
		cfg := readInitConfig(t, h)
		ws, _ := cfg["workspace"].(map[string]any)
		if _, ok := ws["repositories"]; !ok {
			t.Errorf("workspace.repositories key absent from:\n%s", h.Workspace.ReadConfig())
		}
		if got := configStrings(t, cfg, "workspace", "repositories"); len(got) != 0 {
			t.Errorf("repositories = %v, want none", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		want := "no repositories configured — add patterns to " + h.Workspace.ConfigPath()
		ev, ok := h.Reporter.Find(want)
		if !ok {
			t.Fatalf("no unconfigured-repositories step; reported:\n%s", h.Reporter.Dump())
		}
		if ev.Kind != ui.CaptureSkip {
			t.Errorf("step kind = %q, want %q", ev.Kind, ui.CaptureSkip)
		}
	})

	t.Run("workspace_root/from_flag", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r") // repository patterns — still prompted
		skipIntegrationGates(h)

		if err := h.Run("init", "--workspace-root", "worktrees"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		if _, ok := h.Reporter.Find("Workspace Root"); ok {
			t.Errorf("prompted for workspace root despite --workspace-root:\n%s", h.Reporter.Dump())
		}
	})

	t.Run("workspace_root/from_interactive_prompt", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		assertSaved(t, h, "Workspace Root", "worktrees")
	})

	t.Run("integration_groups/declines_all_optional", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		// Declining a gate must write nothing for that group — not
		// even the provider it was defaulting to.
		cfg := readInitConfig(t, h)
		for _, group := range []string{"issue_tracker", "code_host", "notification", "cicd", "preview"} {
			if _, ok := cfg[group]; ok {
				t.Errorf("%s configured despite skipping its gate: %v", group, cfg[group])
			}
		}
	})

	t.Run("integration_groups/configures_one_provider", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		h.Type("\r")       // issue tracker gate — skip
		h.Type("\x1b[B\r") // code host gate — github
		h.Type("\x1b[B\r") // merge method — one past the "squash" default
		h.Type("\r")       // notification gate — skip
		h.Type("\r")       // ci/cd gate — skip
		h.Type("\r")       // preview gate — skip
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		// Presence-only, by construction: every group in configSchema
		// offers exactly one provider, so "the gate's pick was honoured"
		// and "the schema's only option was written" are the same
		// string. This line pins that the group was configured at all —
		// it cannot distinguish a run that discarded the selection.
		// Give it teeth by adding a second provider to the schema.
		if got := configString(t, cfg, "code_host", "provider"); got != "github" {
			t.Errorf("code_host.provider = %q, want %q", got, "github")
		}
		// merge_method is where the discrimination lives: "merge" is one
		// past the schema default "squash", so this fails if the form's
		// selection is discarded.
		if got := configString(t, cfg, "code_host", "merge_method"); got != "merge" {
			t.Errorf("code_host.merge_method = %q, want %q", got, "merge")
		}
		// Configuring one group leaves its siblings alone.
		for _, group := range []string{"issue_tracker", "notification", "cicd", "preview"} {
			if _, ok := cfg[group]; ok {
				t.Errorf("%s configured despite skipping its gate: %v", group, cfg[group])
			}
		}
	})

	// The teeth the scenario above asks for. Preview is the first group
	// with two providers, so picking the second one distinguishes "the
	// gate's pick was honoured" from "the schema's only option was
	// written" — every other group collapses those two into the same
	// string.
	t.Run("integration_groups/honours_a_pick_among_several_providers", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		h.Type("\r") // issue tracker gate — skip
		h.Type("\r") // code host gate — skip
		h.Type("\r") // notification gate — skip
		h.Type("\r") // ci/cd gate — skip
		// Two past "- skip -": cicd is the default this key falls back
		// to, so landing on it would also be what a discarded selection
		// produced.
		h.Type("\x1b[B\x1b[B\r")
		h.Type("\x15https://ephemeral.test\r") // API base URL — example cleared, replaced
		h.Type("\r")                           // API auth mode — the sole option
		h.Type("\x15\r")                       // preview URL template — cleared, left unset
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configString(t, cfg, "preview", "provider"); got != "ephemeral" {
			t.Errorf("preview.provider = %q, want ephemeral", got)
		}
		// The adapter's own keys reached the form from its descriptor,
		// which is the whole point of registering it: nothing in the
		// schema names them.
		preview, ok := cfg["preview"].(map[string]any)
		if !ok {
			t.Fatalf("preview = %#v, want a map", cfg["preview"])
		}
		// A provider's keys sit directly in its capability's block, not
		// in a sub-namespace of it.
		if got := preview["base_url"]; got != "https://ephemeral.test" {
			t.Errorf("preview.base_url = %v, want https://ephemeral.test", got)
		}
		if got := preview["auth"]; got != "gh-cli" {
			t.Errorf("preview.auth = %v, want gh-cli", got)
		}
	})

	t.Run("integration_groups/provider_only_skips_field_form", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		h.Type("\r")       // issue tracker gate — skip
		h.Type("\r")       // code host gate — skip
		h.Type("\r")       // notification gate — skip
		h.Type("\x1b[B\r") // ci/cd gate — github_actions
		// No per-field form follows: cicd is a ProviderOnly group, so
		// its workflow keys stay unasked. A form here would consume
		// the preview gate's keystroke and abort the run.
		h.Type("\r") // preview gate — skip
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		group, ok := cfg["cicd"].(map[string]any)
		if !ok {
			t.Fatalf("cicd = %#v, want a map", cfg["cicd"])
		}
		// The key set is the assertion with teeth: ProviderOnly means the
		// eight workflow keys stay unasked and unwritten.
		if got := sortedKeys(group); !reflect.DeepEqual(got, []string{"provider"}) {
			t.Errorf("cicd keys = %v, want [provider]", got)
		}
		// Presence-only for the same reason as code_host.provider above:
		// cicd offers exactly one provider, so this can't fail while the
		// group is configured at all.
		if group["provider"] != "github_actions" {
			t.Errorf("cicd.provider = %v, want github_actions", group["provider"])
		}
	})

	t.Run("integration_groups/form_fields_arrive_prefilled", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		h.Type("\x1b[B\r") // issue tracker gate — jira
		// The per-field form now runs over the group's non-provider,
		// non-secret keys in schema order. Unlike the project-settings
		// prompts (placeholder-style: blank accepts the default), these
		// fields arrive with the current/default/example value already
		// IN the buffer, so typing appends to it. \x15 (ctrl+u) clears
		// the line first.
		h.Type("\x15https://acme.test\r") // base URL — cleared, replaced
		h.Type("dev@acme.test\r")         // email — no example, so nothing to clear
		h.Type("\x15ACME\r")              // project key — cleared, replaced
		h.Type("\r")                      // board ID — accept the example as-is
		// No issue-pattern field: it is NoPrompt. If that regresses,
		// this "\r" feeds it instead of the code-host gate and the run
		// walks off the end of the queued input into the tripwire.
		h.Type("\r") // code host gate — skip
		h.Type("\r") // notification gate — skip
		h.Type("\r") // ci/cd gate — skip
		h.Type("\r") // preview gate — skip
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		// Had \x15 not cleared the field, this would be the schema
		// example with the typed text appended to it.
		if got := configString(t, cfg, "issue_tracker", "base_url"); got != "https://acme.test" {
			t.Errorf("issue_tracker.base_url = %q, want %q", got, "https://acme.test")
		}
		if got := configString(t, cfg, "issue_tracker", "email"); got != "dev@acme.test" {
			t.Errorf("issue_tracker.email = %q, want %q", got, "dev@acme.test")
		}
		if got := configString(t, cfg, "issue_tracker", "project"); got != "ACME" {
			t.Errorf("issue_tracker.project = %q, want %q", got, "ACME")
		}
		// An untouched field persists its prefill — the schema example
		// becomes a real configured value, which is the flip side of the
		// prefill behavior and the reason \x15 matters at all.
		if got := configString(t, cfg, "issue_tracker", "board_id"); got != "123" {
			t.Errorf("issue_tracker.board_id = %q, want the example %q", got, "123")
		}
		// Secrets are filtered out of the form — the token lives in
		// BOSUN_JIRA_TOKEN, never in the file.
		group, ok := cfg["issue_tracker"].(map[string]any)
		if !ok {
			t.Fatalf("issue_tracker = %#v, want a map", cfg["issue_tracker"])
		}
		if _, ok := group["token"]; ok {
			t.Errorf("issue_tracker.token written to config: %v", group)
		}
		// issue_pattern is never offered and never written. Unset means
		// "use the tracker's own key grammar", so an example regex
		// persisted by a user tabbing through the form would pin one
		// tracker's shape onto whatever tracker is configured.
		if _, ok := group["issue_pattern"]; ok {
			t.Errorf("issue_tracker.issue_pattern written by the init form: %v", group)
		}
	})

	t.Run("already_initialized/prompts_to_reconfigure", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		h.Type("y")           // Already Initialized — reconfigure
		h.Type("src/*\r")     // repository patterns
		h.Type("worktrees\r") // workspace root
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		// Reinit does targeted updates: service configuration the user
		// didn't revisit survives the rewrite.
		if got := configString(t, cfg, "issue_tracker", "base_url"); got != "https://acme.atlassian.net" {
			t.Errorf("issue_tracker.base_url = %q, want it preserved", got)
		}
		if got := configString(t, cfg, "issue_tracker", "project"); got != "ACME" {
			t.Errorf("issue_tracker.project = %q, want it preserved", got)
		}
		assertWorkflowsPreserved(t, cfg)
	})

	t.Run("already_initialized/reconfigure_defaults_to_the_existing_values", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		h.Type("y") // Already Initialized — reconfigure
		// Enter through both prompts without typing. This is the
		// "reconfigure, but keep what I have" path, and it is the only
		// scenario that exercises the reinit default chain at
		// init.go:112-121 — every other reinit scenario types over the
		// prompt or takes --quick's separate reuse branch. Without that
		// chain the placeholders are the fresh-project fallbacks, so
		// pressing Enter would silently REPLACE the user's settings
		// with "., ./*" and ".workspaces".
		h.Type("\r") // repository patterns — accept the placeholder
		h.Type("\r") // workspace root — accept the placeholder
		skipIntegrationGates(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"repos/*"}) {
			t.Errorf("repositories = %v, want [repos/*]", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != ".wt" {
			t.Errorf("workspace.root = %q, want %q", got, ".wt")
		}
		// The values were echoed back as the resolved placeholders, not
		// typed — which is what tells this apart from a run that
		// prompted with the wrong default and happened to be accepted.
		assertSaved(t, h, "Repository Patterns", "repos/*")
		assertSaved(t, h, "Workspace Root", ".wt")
		assertWorkflowsPreserved(t, cfg)
	})

	t.Run("already_initialized/reconfigure_prompt_defaults_to_declining", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		// Enter on the dialog takes the focused button. It must be the
		// declining one: this prompt gates a rewrite of a config the
		// user already has, so the unconsidered keystroke has to be the
		// one that changes nothing. approve_flag_skips_reconfigure_prompt
		// leans on this too — its ability to fail rather than hang
		// assumes an unanswered dialog declines.
		h.Type("\r")
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, ok := h.Reporter.Find("keeping existing configuration"); !ok {
			t.Errorf("no keeping-existing-configuration step; reported:\n%s", h.Reporter.Dump())
		}
		if got := h.Workspace.ReadConfig(); got != configuredProject {
			t.Errorf("config rewritten after accepting the default:\n%s", got)
		}
	})

	t.Run("already_initialized/declines_reconfigure_keeps_config", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		h.Type("n") // Already Initialized — decline
		// Nothing past the dialog should run.
		tripwire(h)

		// Declining is a clean no-op, not a cancellation: the command
		// succeeds and says why it stopped.
		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, ok := h.Reporter.Find("keeping existing configuration"); !ok {
			t.Errorf("no keeping-existing-configuration step; reported:\n%s", h.Reporter.Dump())
		}
		if got := h.Workspace.ReadConfig(); got != configuredProject {
			t.Errorf("config rewritten after declining:\n%s", got)
		}
	})

	t.Run("already_initialized/approve_flag_skips_reconfigure_prompt", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		// No answer queued for the reinit dialog. If it still runs it
		// reads the repository-patterns keys instead, and its focused
		// button (Default(false)) declines — leaving the config as
		// seeded and failing the assertion below rather than hanging.
		h.Type("src/*\r")
		h.Type("worktrees\r")
		skipIntegrationGates(h)

		if err := h.Run("init", "--approve"); err != nil {
			t.Fatalf("init: %v", err)
		}

		if _, ok := h.Reporter.Find("keeping existing configuration"); ok {
			t.Fatalf("reinit dialog ran despite --approve:\n%s", h.Reporter.Dump())
		}
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		// --approve suppresses the question, not the reinit itself:
		// the run still takes the targeted-update path, so existing
		// service configuration survives. Without this the scenario
		// would also pass against a run that never recognized the
		// project as initialized at all.
		if got := configString(t, cfg, "issue_tracker", "project"); got != "ACME" {
			t.Errorf("issue_tracker.project = %q, want it preserved", got)
		}
		assertWorkflowsPreserved(t, cfg)
	})

	t.Run("already_initialized/nested_project_rejected", func(t *testing.T) {
		h := testharness.New(t)
		// The enclosing project is built by hand INSIDE an
		// uninitialized workspace rather than being the workspace
		// itself. That is the whole point of the scenario: while
		// h.Workspace has no .bosun/, Harness.Run injects no --project,
		// so config.ProjectRootOverride stays empty and
		// config.FindProjectRoot() has to discover the project by
		// walking up from the working directory — the path a real
		// `bosun init` takes. Point --project at the outer project
		// instead and the guard fires on the override, leaving the walk
		// (and therefore the production behavior) untested.
		h.Workspace.Uninitialize()
		outer := filepath.Join(h.Workspace.Dir, "outer")
		if err := os.MkdirAll(filepath.Join(outer, ".bosun"), 0o755); err != nil {
			t.Fatalf("create outer project: %v", err)
		}
		outerConfig := filepath.Join(outer, ".bosun", "config.yaml")
		if err := os.WriteFile(outerConfig, []byte(configuredProject), 0o644); err != nil {
			t.Fatalf("write outer config: %v", err)
		}
		nested := filepath.Join(outer, "nested")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatalf("create nested dir: %v", err)
		}
		t.Chdir(nested)

		tripwire(h) // nothing here should prompt

		err := h.Run("init")
		if err == nil {
			t.Fatalf("init inside an existing project succeeded")
		}
		if !strings.Contains(err.Error(), "nested projects are not supported") {
			t.Errorf("err = %v, want a nested-project rejection", err)
		}
		// The message names the project it found, which is how the user
		// knows which directory to go argue with. It is also what
		// distinguishes a discovered root from an injected one.
		if !strings.Contains(err.Error(), outer) {
			t.Errorf("err = %v, want it to name the enclosing project %s", err, outer)
		}
		if _, statErr := os.Stat(filepath.Join(nested, ".bosun")); statErr == nil {
			t.Errorf("nested .bosun/ created")
		}
	})

	t.Run("quick_flag/reuses_existing_project_settings", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		// Quick mode on a fully-configured project has nothing left to
		// ask: project settings come from the existing config and every
		// service key already has a value. The tripwire turns a
		// regression that re-prompts into an abort instead of a hang.
		tripwire(h)

		if err := h.Run("init", "--quick", "--approve"); err != nil {
			t.Fatalf("init: %v", err)
		}

		// A preservation scenario has a structural blind spot: the
		// correct end state is the state it started in, so every
		// value-level assertion below is equally happy with a run that
		// did nothing at all (`if quick { return nil }` at the top of
		// runInit used to sail through them). This is the assertion that
		// closes it. Reinit's targeted updates go through viper —
		// ReadInConfig, Set, WriteConfigAs — which reserializes the
		// whole document, so a run that reached the write leaves a file
		// that is no longer byte-identical to the seed even though every
		// value in it is. Nothing else here can tell the two apart.
		if got := h.Workspace.ReadConfig(); got == configuredProject {
			t.Errorf("config is byte-identical to the seed — init never wrote:\n%s", got)
		}

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"repos/*"}) {
			t.Errorf("repositories = %v, want [repos/*]", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != ".wt" {
			t.Errorf("workspace.root = %q, want %q", got, ".wt")
		}
		if got := configString(t, cfg, "issue_tracker", "project"); got != "ACME" {
			t.Errorf("issue_tracker.project = %q, want it preserved", got)
		}
		// Non-default on purpose: quick mode resolves a group's unset
		// keys, and this one is set, so it must come back untouched
		// rather than reverting to the schema's "squash".
		if got := configString(t, cfg, "code_host", "merge_method"); got != "rebase" {
			t.Errorf("code_host.merge_method = %q, want it preserved", got)
		}
		assertWorkflowsPreserved(t, cfg)
	})

	t.Run("quick_flag/dry_run_reports_the_reused_settings", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		tripwire(h) // quick + a configured project asks nothing

		if err := h.Run("init", "--quick", "--approve", "--dry-run"); err != nil {
			t.Fatalf("init: %v", err)
		}

		// The dry-run preview is where quick mode's reuse becomes
		// directly observable rather than merely indistinguishable from
		// the seed: these rows are rendered from the in-memory
		// repositoryGlobs / wsRoot that the quick+reinit branch loaded
		// out of the existing config. A run that skipped that branch
		// reports different rows (or, having prompted for them instead,
		// trips the tripwire) — where the on-disk assertions above would
		// still have found the seeded values sitting untouched.
		details := h.Reporter.OfKind(ui.CaptureDetails)
		if len(details) != 1 {
			t.Fatalf("details blocks = %d, want 1; reported:\n%s", len(details), h.Reporter.Dump())
		}
		want := []string{
			"Config: .bosun/config.yaml",
			"Repositories: repos/*",
			"Workspace Root: .wt",
		}
		if got := details[0].Items; !reflect.DeepEqual(got, want) {
			t.Errorf("details = %v, want %v", got, want)
		}
		if got := h.Workspace.ReadConfig(); got != configuredProject {
			t.Errorf("config rewritten during --dry-run:\n%s", got)
		}
	})

	t.Run("quick_flag/prompts_only_for_a_missing_required_key", func(t *testing.T) {
		h := testharness.New(t)
		// Everything the fixture has, minus one required key. Quick mode
		// skips the four provider gates entirely and calls resolveGroup,
		// which is just-in-time: keys that already have values are left
		// alone and only the gap is asked about.
		h.Workspace.WriteConfig(strings.Replace(configuredProject,
			"  email: dev@acme.test\n", "", 1))
		t.Chdir(h.Workspace.Dir)

		h.Type("dev@acme.test\r") // the one gap — issue_tracker.email
		// No gate keystrokes queued. If quick mode stopped bypassing the
		// gates, the issue-tracker gate would eat the line above and the
		// code-host gate would hit the tripwire.
		tripwire(h)

		if err := h.Run("init", "--quick", "--approve"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configString(t, cfg, "issue_tracker", "email"); got != "dev@acme.test" {
			t.Errorf("issue_tracker.email = %q, want it filled in", got)
		}
		assertSaved(t, h, "email", "dev@acme.test")
		// resolveGroup, not resolveGroupReconfigure: the keys that were
		// already set are neither re-asked nor rewritten.
		if got := configString(t, cfg, "issue_tracker", "base_url"); got != "https://acme.atlassian.net" {
			t.Errorf("issue_tracker.base_url = %q, want it preserved", got)
		}
		if got := configString(t, cfg, "code_host", "merge_method"); got != "rebase" {
			t.Errorf("code_host.merge_method = %q, want it preserved", got)
		}
		if got := configString(t, cfg, "notification", "auth"); got != "local" {
			t.Errorf("notification.auth = %q, want it preserved", got)
		}
		assertWorkflowsPreserved(t, cfg)
	})

	t.Run("quick_flag/fresh_project_still_asks_for_project_settings", func(t *testing.T) {
		h := newInitHarness(t)

		// Quick mode's reuse is conditional on reinit. On a fresh
		// project there is nothing to reuse, so both project settings
		// are still prompted for — --quick trims the optional service
		// wizard, not the required values. Stopping at --dry-run keeps
		// the scenario to those two prompts instead of the ~20 the full
		// quick wizard would ask on an empty config.
		h.Type("src/*\r")
		h.Type("worktrees\r")
		tripwire(h)

		if err := h.Run("init", "--quick", "--dry-run"); err != nil {
			t.Fatalf("init: %v", err)
		}

		details := h.Reporter.OfKind(ui.CaptureDetails)
		if len(details) != 1 {
			t.Fatalf("details blocks = %d, want 1; reported:\n%s", len(details), h.Reporter.Dump())
		}
		want := []string{
			"Config: .bosun/config.yaml",
			"Repositories: src/*",
			"Workspace Root: worktrees",
		}
		if got := details[0].Items; !reflect.DeepEqual(got, want) {
			t.Errorf("details = %v, want %v", got, want)
		}
		assertSaved(t, h, "Repository Patterns", "src/*")
		assertSaved(t, h, "Workspace Root", "worktrees")
	})

	t.Run("dry_run/skips_apply", func(t *testing.T) {
		h := newInitHarness(t)

		// Both project settings come from flags, so the dry-run gate is
		// reached without a prompt; the tripwire catches it if one
		// sneaks in.
		tripwire(h)

		err := h.Run("init", "--dry-run",
			"--repositories", "src/*", "--workspace-root", "worktrees")
		if err != nil {
			t.Fatalf("init: %v", err)
		}

		if h.Workspace.Initialized() {
			t.Errorf(".bosun/ created during --dry-run")
		}
		ev, ok := h.Reporter.Find("would initialize bosun project")
		if !ok {
			t.Fatalf("no dry-run step; reported:\n%s", h.Reporter.Dump())
		}
		if ev.Kind != ui.CaptureDryRun {
			t.Errorf("dry-run step kind = %q, want %q", ev.Kind, ui.CaptureDryRun)
		}
		// The preview names what would have been written, including the
		// resolved values — otherwise --dry-run tells the user nothing.
		details := h.Reporter.OfKind(ui.CaptureDetails)
		if len(details) != 1 {
			t.Fatalf("details blocks = %d, want 1; reported:\n%s", len(details), h.Reporter.Dump())
		}
		want := []string{
			"Config: .bosun/config.yaml",
			"Repositories: src/*",
			"Workspace Root: worktrees",
		}
		if got := details[0].Items; !reflect.DeepEqual(got, want) {
			t.Errorf("details = %v, want %v", got, want)
		}
	})

	t.Run("errors/cancelled_at_integration_gate_keeps_project_settings", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("src/*\r")
		h.Type("worktrees\r")
		h.Type("\x03") // ctrl+c at the issue-tracker gate
		// Sentinel, not input — nothing on the intended path reads past
		// the ctrl+c. It exists because the regression this scenario is
		// here to catch (runForm's huh.ErrUserAborted -> ErrCancelled
		// mapping breaking, or the abort being swallowed) would
		// otherwise run on into the next gate against a drained reader
		// and HANG rather than fail. These let the run finish instead,
		// so the ErrCancelled assertion below reports err = nil.
		//
		// A second \x03 would be the obvious sentinel and is the wrong
		// one here: it would cancel at the code-host gate and every
		// assertion in this scenario would still hold, masking exactly
		// the bug it was added for. The sentinel has to let the run
		// SUCCEED to be discriminating.
		for range integrationGateCount - 1 { // every gate after the cancelled one
			h.Type("\r")
		}
		tripwire(h)

		err := h.Run("init")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("err = %v, want ErrCancelled", err)
		}
		// Init isn't transactional: project settings are on disk
		// before the service wizard starts, so cancelling partway
		// through leaves a usable — if unfinished — config rather
		// than nothing.
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "workspace", "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		if _, ok := cfg["issue_tracker"]; ok {
			t.Errorf("issue_tracker written despite cancelling its gate: %v", cfg["issue_tracker"])
		}
	})

	t.Run("errors/cancelled_at_reconfigure_dialog", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		t.Chdir(h.Workspace.Dir)

		h.Type("\x03") // ctrl+c at the Already Initialized dialog
		// Sentinel — see errors/cancelled_at_integration_gate. The keys
		// a run that ignored the abort would need, so it finishes and
		// fails the assertions instead of blocking.
		h.Type("src/*\r")
		h.Type("worktrees\r")
		skipIntegrationGates(h)

		err := h.Run("init")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("err = %v, want ErrCancelled", err)
		}
		// Aborting the question is not the same as answering "no" to
		// it: declining reports why it stopped, cancelling just stops.
		if _, ok := h.Reporter.Find("keeping existing configuration"); ok {
			t.Errorf("reported a decline for a cancellation:\n%s", h.Reporter.Dump())
		}
		if got := h.Workspace.ReadConfig(); got != configuredProject {
			t.Errorf("config rewritten after cancelling:\n%s", got)
		}
	})

	t.Run("errors/cancelled_at_prompt_aborts", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("\x03") // ctrl+c at the repository-patterns prompt
		// Sentinel — same reasoning as the integration-gate scenario
		// above: let a swallowed abort run to completion so this fails
		// instead of hanging on a drained reader. The discriminating
		// assertion is the .bosun/ check, which a completed run trips
		// no matter which of these keys it happened to consume; a bare
		// second \x03 would abort at the workspace-root prompt and
		// still look like a clean cancellation.
		h.Type("\r") // workspace root
		h.Type("\r") // second chance at repository patterns
		skipIntegrationGates(h)

		err := h.Run("init")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("err = %v, want ErrCancelled", err)
		}
		if h.Workspace.Initialized() {
			t.Errorf(".bosun/ created after cancelling")
		}
	})
}

// configuredProject is a project config with every service group
// fully populated — the fixture for reinit scenarios. Values are
// deliberately unlike anything the scenarios type in, and unlike the
// schema defaults, so "preserved" and "rewritten" are distinguishable.
//
// That second property is load-bearing rather than tidy. A reinit
// scenario asserts that an existing value survived; if the fixture
// happens to hold what the code would have produced anyway, the
// assertion passes whether or not anything was preserved. Four values
// previously collided and have all been moved off their defaults:
// `workspace.root` (init.go's hardcoded ".workspaces" fallback),
// `code_host.merge_method` ("squash"), `notification.auth` ("token")
// and `cicd.workflows.release.inputs.version` ("version"). Every
// non-provider value below is now one neither path can invent. Keep it
// that way when editing: grep the key in schema.go for a Default
// before choosing.
//
// The four `provider` values are the exception and cannot be fixed
// here: each is the only entry in its group's schema Options, so it is
// exactly what init would write if it regenerated the group. Don't
// assert provider preservation on this fixture — such an assertion
// would pass whether or not anything was preserved. Give it teeth by
// adding a second provider to configSchema, not by editing this.
//
// Completeness matters for the quick-mode scenario specifically:
// quick resolves each service group's unset keys by prompting, so a
// gap here becomes an unfed prompt.
const configuredProject = `workspace:
  repositories:
    - repos/*
  root: .wt
issue_tracker:
  provider: jira
  base_url: https://acme.atlassian.net
  email: dev@acme.test
  token: seeded-jira-token
  project: ACME
  board_id: "42"
  issue_pattern: '(ACME-[0-9]+)'
code_host:
  provider: github
  token: seeded-gh-token
  merge_method: rebase
notification:
  provider: slack
  auth: local
  token: seeded-slack-token
  workspace: acme
  channels:
    review: reviews
    prerelease: releases
cicd:
  provider: github_actions
  workflows:
    release:
      target: acme/infra/.github/workflows/release.yml
      inputs:
        version: release_version
preview:
  url_template: https://preview-{{.Name}}.acme.test
  up:
    workflow: acme/infra/.github/workflows/up.yml
    inputs:
      name: name
  down:
    workflow: acme/infra/.github/workflows/down.yml
    inputs:
      name: name
`

// configuredWorkflows and configuredPreview are configuredProject's two
// deepest nested structures in parsed form, and the ones most at risk
// from reinit's write path. Reinit rewrites the WHOLE file through
// viper (ReadInConfig, Set, WriteConfigAs) to update two keys, so every
// untouched branch survives only because viper's round-trip happens to
// preserve it. Asserting on the trees wholesale rather than on a leaf
// means a serializer change that flattened, reordered into strings, or
// dropped a level fails here instead of shipping.
var configuredWorkflows = map[string]any{
	"release": map[string]any{
		"target": "acme/infra/.github/workflows/release.yml",
		"inputs": map[string]any{"version": "release_version"},
	},
}

var configuredPreview = map[string]any{
	"url_template": "https://preview-{{.Name}}.acme.test",
	"up": map[string]any{
		"workflow": "acme/infra/.github/workflows/up.yml",
		"inputs":   map[string]any{"name": "name"},
	},
	"down": map[string]any{
		"workflow": "acme/infra/.github/workflows/down.yml",
		"inputs":   map[string]any{"name": "name"},
	},
}

// assertWorkflowsPreserved checks that the seeded cicd.workflows and
// preview trees came back byte-for-byte after a reinit. Use it in any
// scenario that re-runs init over configuredProject without revisiting
// CI/CD or preview.
func assertWorkflowsPreserved(t *testing.T, cfg map[string]any) {
	t.Helper()
	group, ok := cfg["cicd"].(map[string]any)
	if !ok {
		t.Fatalf("cicd = %#v, want a map", cfg["cicd"])
	}
	if got := group["workflows"]; !reflect.DeepEqual(got, configuredWorkflows) {
		t.Errorf("cicd.workflows = %#v,\nwant %#v", got, configuredWorkflows)
	}
	if got := cfg["preview"]; !reflect.DeepEqual(got, configuredPreview) {
		t.Errorf("preview = %#v,\nwant %#v", got, configuredPreview)
	}
}

// newInitHarness builds a harness whose workspace is an ordinary
// empty directory: .bosun/ removed (so init takes its fresh-project
// path rather than reinit) and the process CWD pointed at it (init
// resolves the project from os.Getwd, not --project).
func newInitHarness(t *testing.T) *testharness.Harness {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.Uninitialize()
	t.Chdir(h.Workspace.Dir)
	return h
}

// skipIntegrationGates queues the keystroke that declines each of the
// optional service gates init offers after writing project settings.
// Every non-quick scenario needs these regardless of what it's
// actually about — the gates render whenever the session is
// interactive, and the harness always is. It closes with a tripwire,
// so it is also the last input a scenario using it queues.
func skipIntegrationGates(h *testharness.Harness) {
	for range integrationGateCount {
		h.Type("\r") // "- skip -" is the default-focused option
	}
	tripwire(h)
}

// integrationGateCount is how many provider gates the wizard offers —
// one per entry in serviceInitGroups (init.go): issue tracker, code
// host, notifications, CI/CD, preview.
//
// Restated here rather than read off that slice because these scenarios
// are an external test package. Adding a group without bumping this
// leaves every scenario one keystroke short, and the symptom is
// "init: cancelled" from the tripwire — which names the scenario, not
// the group that was added, so start here when that appears.
const integrationGateCount = 5

// tripwire queues a ctrl+c as a scenario's final input. Nothing
// should read it: it is there so a regression that reaches a prompt
// the scenario didn't plan for aborts the run instead of blocking
// forever on a drained reader — which would burn the whole package's
// timeout and take every other test's result down with it. Queue it
// last, always.
func tripwire(h *testharness.Harness) { h.Type("\x03") }

// displayPath is how the command renders a path in a confirmation
// line — cli.shortPath's rule, which abbreviates a $HOME prefix to "~".
// Mirrored rather than reached for because shortPath is unexported;
// the assertion it serves is about WHICH file the command named, not
// about how the path was formatted, so restating the rule here costs
// nothing it was covering. A t.TempDir path normally sits outside
// $HOME and comes back unchanged; this only matters when TMPDIR
// doesn't.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// addChildRepo creates a git repository directly under the workspace
// root, where init's auto-detection scans (it looks one level down,
// not into repos/ the way Workspace.AddRepo lays things out).
func addChildRepo(t *testing.T, h *testharness.Harness, name string) {
	t.Helper()
	dir := filepath.Join(h.Workspace.Dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	testharness.Git(t, dir, "init")
}

// readInitConfig parses the written .bosun/config.yaml into a nested
// map. Scenarios assert on this rather than on the file's text: a
// fresh config's commented-out template mentions every provider key
// by name, so substring matching would confirm settings that were
// never made.
func readInitConfig(t *testing.T, h *testharness.Harness) map[string]any {
	t.Helper()
	raw := h.Workspace.ReadConfig()
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parse config: %v\n%s", err, raw)
	}
	return cfg
}

// configString reads a nested string value, failing the test if the
// path exists but isn't a string. A missing path returns "" so the
// caller's comparison reports the whole mismatch.
func configString(t *testing.T, cfg map[string]any, path ...string) string {
	t.Helper()
	cur := any(cfg)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = m[key]
		if !ok {
			return ""
		}
	}
	if cur == nil {
		return ""
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("%v = %#v, want a string", path, cur)
	}
	return s
}

// configStrings reads a nested list of strings
// (workspace.repositories).
func configStrings(t *testing.T, cfg map[string]any, path ...string) []string {
	t.Helper()
	cur := any(cfg)
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[key]
		if !ok {
			return nil
		}
	}
	if cur == nil {
		// `repositories:` with nothing but comments beneath it — the
		// template init writes when it has no patterns to record.
		return nil
	}
	items, ok := cur.([]any)
	if !ok {
		t.Fatalf("%v = %#v, want a list", path, cur)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			t.Fatalf("%v contains %#v, want strings", path, it)
		}
		out = append(out, s)
	}
	return out
}

// topLevelKeys returns cfg's keys in sorted order.
func topLevelKeys(cfg map[string]any) []string { return sortedKeys(cfg) }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assertSaved checks that the command echoed a field back with the
// value the run resolved. The Saved record is the only observable
// difference between a value that came from a PROMPT and one that
// came from a flag or from the skip path — it says the prompt ran,
// not that the user typed. init emits it with defaultField.Resolved(),
// which is the placeholder when the entry was blank, so a scenario
// that presses Enter through a prompt still gets a Saved record and
// the value in it is the default that was offered. That makes it the
// assertion for "the prompt offered the right default" as well.
func assertSaved(t *testing.T, h *testharness.Harness, label, value string) {
	t.Helper()
	ev, ok := h.Reporter.Find(label)
	if !ok {
		t.Errorf("no %q step; reported:\n%s", label, h.Reporter.Dump())
		return
	}
	if ev.Kind != ui.CaptureSaved {
		t.Errorf("%q kind = %q, want %q", label, ev.Kind, ui.CaptureSaved)
	}
	if ev.Value != value {
		t.Errorf("%q value = %q, want %q", label, ev.Value, value)
	}
}
