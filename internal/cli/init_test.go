package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

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
// First, init is the one command that ignores --project: it writes
// `.bosun/` relative to os.Getwd(), and refuses to nest inside an
// existing project. Every scenario therefore t.Chdir's into the
// workspace, and fresh-project scenarios call Uninitialize first
// (NewWorkspace creates .bosun/ for every other command's benefit,
// which init would read as a reinit).
//
// Second, the harness counts as interactive (ui.Interactive is true
// for any injected reader), so the optional-service wizard runs on
// every non-quick path — four provider gates that each need a
// keystroke even when the scenario is about something else. Skipping
// a gate is a bare "\r" on its select.
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		if got := configString(t, cfg, "workspace", "root"); got != "worktrees" {
			t.Errorf("workspace.root = %q, want %q", got, "worktrees")
		}
		// A minimum config is exactly the two project settings — the
		// integration groups the wizard offered are all declined, and
		// the template's commented-out examples must not materialize
		// as real keys.
		if got := topLevelKeys(cfg); !reflect.DeepEqual(got, []string{"repositories", "workspace"}) {
			t.Errorf("top-level keys = %v, want [repositories workspace]", got)
		}
		assertSaved(t, h, "Repository Patterns", "src/*")
		assertSaved(t, h, "Workspace Root", "worktrees")
	})

	t.Run("fresh_project/detects_repos_from_cwd", func(t *testing.T) {
		h := newInitHarness(t)
		addChildRepo(t, h, "api")
		addChildRepo(t, h, "web")

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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"./*"}) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"."}) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{".", "./*"}) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, want) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, want) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"fallback/*"}) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"./*"}) {
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
		if _, ok := cfg["repositories"]; !ok {
			t.Errorf("repositories key absent from:\n%s", h.Workspace.ReadConfig())
		}
		if got := configStrings(t, cfg, "repositories"); len(got) != 0 {
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
		for _, group := range []string{"issue_tracker", "code_host", "notification", "cicd"} {
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
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		if got := configString(t, cfg, "code_host", "provider"); got != "github" {
			t.Errorf("code_host.provider = %q, want %q", got, "github")
		}
		// "merge" rather than the schema default "squash", so the
		// assertion fails if the form's selection is discarded.
		if got := configString(t, cfg, "code_host", "merge_method"); got != "merge" {
			t.Errorf("code_host.merge_method = %q, want %q", got, "merge")
		}
		// Configuring one group leaves its siblings alone.
		for _, group := range []string{"issue_tracker", "notification", "cicd"} {
			if _, ok := cfg[group]; ok {
				t.Errorf("%s configured despite skipping its gate: %v", group, cfg[group])
			}
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
		// the tripwire and abort the run.
		tripwire(h)

		if err := h.Run("init"); err != nil {
			t.Fatalf("init: %v", err)
		}

		cfg := readInitConfig(t, h)
		group, ok := cfg["cicd"].(map[string]any)
		if !ok {
			t.Fatalf("cicd = %#v, want a map", cfg["cicd"])
		}
		if got := sortedKeys(group); !reflect.DeepEqual(got, []string{"provider"}) {
			t.Errorf("cicd keys = %v, want [provider]", got)
		}
		if group["provider"] != "github_actions" {
			t.Errorf("cicd.provider = %v, want github_actions", group["provider"])
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
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
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
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
	})

	t.Run("already_initialized/nested_project_rejected", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(configuredProject)
		nested := filepath.Join(h.Workspace.Dir, "nested")
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

		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"repos/*"}) {
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

		err := h.Run("init")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("err = %v, want ErrCancelled", err)
		}
		// Init isn't transactional: project settings are on disk
		// before the service wizard starts, so cancelling partway
		// through leaves a usable — if unfinished — config rather
		// than nothing.
		cfg := readInitConfig(t, h)
		if got := configStrings(t, cfg, "repositories"); !reflect.DeepEqual(got, []string{"src/*"}) {
			t.Errorf("repositories = %v, want [src/*]", got)
		}
		if _, ok := cfg["issue_tracker"]; ok {
			t.Errorf("issue_tracker written despite cancelling its gate: %v", cfg["issue_tracker"])
		}
	})

	t.Run("errors/cancelled_at_prompt_aborts", func(t *testing.T) {
		h := newInitHarness(t)

		h.Type("\x03") // ctrl+c at the repository-patterns prompt

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
// assertion passes whether or not anything was preserved. `workspace.
// root` and `code_host.merge_method` are the two that previously
// collided — with init.go's hardcoded ".workspaces" fallback and the
// schema's "squash" default respectively — so both are now values
// neither path can invent. Keep it that way when editing.
//
// Completeness matters for the quick-mode scenario specifically:
// quick resolves each service group's unset keys by prompting, so a
// gap here becomes an unfed prompt.
const configuredProject = `repositories:
  - repos/*
workspace:
  root: .wt
issue_tracker:
  provider: jira
  base_url: https://acme.atlassian.net
  email: dev@acme.test
  token: seeded-jira-token
  project: ACME
  board_id: "42"
code_host:
  provider: github
  token: seeded-gh-token
  merge_method: rebase
notification:
  provider: slack
  auth: token
  token: seeded-slack-token
  workspace: acme
  channel_review: reviews
  channel_prerelease: releases
cicd:
  provider: github_actions
  workflows:
    preview:
      url_template: https://preview-{{.Name}}.acme.test
      up:
        target: acme/infra/.github/workflows/up.yml
        inputs:
          services: services
          name: name
      down:
        target: acme/infra/.github/workflows/down.yml
        inputs:
          name: name
    release:
      target: acme/infra/.github/workflows/release.yml
      inputs:
        version: version
`

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
	for range 4 { // issue tracker, code host, notifications, CI/CD
		h.Type("\r") // "- skip -" is the default-focused option
	}
	tripwire(h)
}

// tripwire queues a ctrl+c as a scenario's final input. Nothing
// should read it: it is there so a regression that reaches a prompt
// the scenario didn't plan for aborts the run instead of blocking
// forever on a drained reader — which would burn the whole package's
// timeout and take every other test's result down with it. Queue it
// last, always.
func tripwire(h *testharness.Harness) { h.Type("\x03") }

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

// configStrings reads a top-level list of strings (repositories).
func configStrings(t *testing.T, cfg map[string]any, key string) []string {
	t.Helper()
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	if raw == nil {
		// `repositories:` with nothing but comments beneath it — the
		// template init writes when it has no patterns to record.
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a list", key, raw)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			t.Fatalf("%s contains %#v, want strings", key, it)
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
// value the scenario typed. The Saved record is the only observable
// difference between a value that came from a prompt and one that
// came from a flag or a default.
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
