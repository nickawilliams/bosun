package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

const baseConfig = `
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
ui:
  compact_header: true
`

// runConfig sets up a fresh harness with baseConfig and runs `config`
// with the given args. Returns (stdout, runErr) — Run captures the
// rendered output (both cobra streams and direct os.Stdout writes)
// via the harness, so h.Stdout() reflects what the user would see.
func runConfig(t *testing.T, args ...string) (string, error) {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.WriteConfig(baseConfig)
	err := h.Run(append([]string{"config"}, args...)...)
	return h.Stdout(), err
}

// TestConfigGet covers the get command's dispatch matrix on
// (key present?, format, -g?). Each subtest seeds a fresh harness
// because the bootstrap guard caches viper between cases.
func TestConfigGet(t *testing.T) {
	t.Run("scalar key returns raw value", func(t *testing.T) {
		out, err := runConfig(t, "get", "issue_tracker.provider")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got := strings.TrimSpace(out); got != "jira" {
			t.Errorf("stdout = %q, want %q", got, "jira")
		}
	})

	t.Run("group key without format errors with suggestion", func(t *testing.T) {
		_, err := runConfig(t, "get", "issue_tracker")
		if err == nil {
			t.Fatal("expected error for non-scalar without -f")
		}
		if !strings.Contains(err.Error(), "group") || !strings.Contains(err.Error(), "-f") {
			t.Errorf("error %q should mention \"group\" and \"-f\"", err.Error())
		}
	})

	t.Run("group key with -f yaml renders subtree", func(t *testing.T) {
		out, err := runConfig(t, "get", "issue_tracker", "-f", "yaml")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !strings.Contains(out, "provider: jira") || !strings.Contains(out, "base_url:") {
			t.Errorf("yaml output missing expected keys; got:\n%s", out)
		}
	})

	t.Run("no key with -f yaml dumps full config", func(t *testing.T) {
		out, err := runConfig(t, "get", "-f", "yaml")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		// Full effective config includes schema-default groups like branch.
		if !strings.Contains(out, "issue_tracker:") || !strings.Contains(out, "branch:") {
			t.Errorf("expected full config dump; got:\n%s", out)
		}
	})

	t.Run("no key no format errors", func(t *testing.T) {
		_, err := runConfig(t, "get")
		if err == nil {
			t.Fatal("expected error for bare `config get`")
		}
		if !strings.Contains(err.Error(), "specify a key") {
			t.Errorf("error %q should mention \"specify a key\"", err.Error())
		}
	})

	t.Run("missing effective key errors", func(t *testing.T) {
		_, err := runConfig(t, "get", "nonexistent.key")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
	})

	t.Run("missing key with -g exits silently", func(t *testing.T) {
		out, err := runConfig(t, "get", "nonexistent.key", "-g")
		if err != nil {
			t.Fatalf("-g miss should be silent success, got: %v", err)
		}
		if out != "" {
			t.Errorf("expected empty stdout, got: %q", out)
		}
	})

	t.Run("unknown format errors", func(t *testing.T) {
		_, err := runConfig(t, "get", "issue_tracker", "-f", "xml")
		if err == nil {
			t.Fatal("expected error for unknown format")
		}
		if !strings.Contains(err.Error(), "xml") {
			t.Errorf("error %q should mention the bad format", err.Error())
		}
	})
}

// TestConfigShow covers the show command's smaller surface — just the
// human tree view, optionally narrowed by group or to global-only.
func TestConfigShow(t *testing.T) {
	t.Run("bare show renders effective tree", func(t *testing.T) {
		out, err := runConfig(t, "show")
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		if !strings.Contains(out, "issue_tracker") || !strings.Contains(out, "jira") {
			t.Errorf("tree missing expected content; got:\n%s", out)
		}
	})

	t.Run("group filter narrows the tree", func(t *testing.T) {
		out, err := runConfig(t, "show", "issue_tracker")
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		if !strings.Contains(out, "issue_tracker") {
			t.Errorf("expected issue_tracker in output, got:\n%s", out)
		}
		// "branch" and "workspace" are other top-level groups; they must
		// not appear when the filter scopes the tree to issue_tracker.
		if strings.Contains(out, "○ branch") || strings.Contains(out, "○ workspace") {
			t.Errorf("filtered tree leaked unrelated groups; got:\n%s", out)
		}
	})

	t.Run("-o flag is no longer accepted", func(t *testing.T) {
		_, err := runConfig(t, "show", "-o", "yaml")
		if err == nil {
			t.Fatal("expected -o to be unknown after the refactor")
		}
	})

	t.Run("empty result renders explicit empty-state marker", func(t *testing.T) {
		// `workspace` is a known schema group but the test config sets
		// only `issue_tracker` and `ui`, so the global-only view
		// of `workspace` resolves to nothing — must be loud about it,
		// not silently render an empty tree.
		out, err := runConfig(t, "show", "workspace", "-g")
		if err != nil {
			t.Fatalf("show: %v", err)
		}
		if !strings.Contains(out, "no config values to display") {
			t.Errorf("expected empty-state marker; got:\n%s", out)
		}
	})
}

// TestConfigSetUnset covers the write-side dispatch: scalar set,
// subtree set via -f yaml/json, unset success, and the various error
// paths. Multi-step tests (set then get to verify) share a single
// harness because each testharness.New gets a fresh temp project +
// fresh viper, so the set and get would otherwise land on different
// configs.
func TestConfigSetUnset(t *testing.T) {
	t.Run("scalar set writes value and renders confirmation", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(baseConfig)

		if err := h.Run("config", "set", "workspace.root", "workspaces"); err != nil {
			t.Fatalf("set: %v", err)
		}
		out := h.Stdout()
		if !strings.Contains(out, "Wrote") || !strings.Contains(out, "workspace.root") {
			t.Errorf("expected 'Wrote workspace.root ...' confirmation; got:\n%s", out)
		}

		if err := h.Run("config", "get", "workspace.root"); err != nil {
			t.Fatalf("get: %v", err)
		}
		// h.Stdout accumulates across Run calls; trim the confirmation
		// from above and look at the get output appended after.
		got := strings.TrimSpace(strings.TrimPrefix(h.Stdout(), out))
		if got != "workspaces" {
			t.Errorf("get after set = %q, want %q", got, "workspaces")
		}
	})

	t.Run("subtree set with -f yaml writes the parsed map", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(baseConfig)

		if err := h.Run("config", "set", "notification",
			"{provider: slack, channels: [eng, ops]}", "-f", "yaml"); err != nil {
			t.Fatalf("set: %v\nstdout: %s", err, h.Stdout())
		}
		setOut := h.Stdout()

		if err := h.Run("config", "get", "notification.provider"); err != nil {
			t.Fatalf("get provider: %v", err)
		}
		got := strings.TrimSpace(strings.TrimPrefix(h.Stdout(), setOut))
		if got != "slack" {
			t.Errorf("notification.provider = %q, want %q", got, "slack")
		}
	})

	t.Run("subtree set with -f json writes the parsed map", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(baseConfig)

		if err := h.Run("config", "set", "notification",
			`{"provider":"discord"}`, "-f", "json"); err != nil {
			t.Fatalf("set: %v\nstdout: %s", err, h.Stdout())
		}
		setOut := h.Stdout()

		if err := h.Run("config", "get", "notification.provider"); err != nil {
			t.Fatalf("get provider: %v", err)
		}
		got := strings.TrimSpace(strings.TrimPrefix(h.Stdout(), setOut))
		if got != "discord" {
			t.Errorf("notification.provider = %q, want %q", got, "discord")
		}
	})

	t.Run("invalid -f format errors", func(t *testing.T) {
		_, err := runConfig(t, "set", "foo", "bar", "-f", "xml")
		if err == nil {
			t.Fatal("expected error for unknown format")
		}
		if !strings.Contains(err.Error(), "xml") {
			t.Errorf("error %q should mention the bad format", err.Error())
		}
	})

	t.Run("malformed json under -f json errors", func(t *testing.T) {
		_, err := runConfig(t, "set", "foo", "not json", "-f", "json")
		if err == nil {
			t.Fatal("expected error for malformed json")
		}
		if !strings.Contains(err.Error(), "json") {
			t.Errorf("error %q should mention json parsing", err.Error())
		}
	})

	t.Run("unset removes the key and renders confirmation", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(baseConfig)

		// baseConfig already has issue_tracker.provider set, so unset has
		// something real to remove.
		if err := h.Run("config", "unset", "issue_tracker.provider"); err != nil {
			t.Fatalf("unset: %v", err)
		}
		out := h.Stdout()
		if !strings.Contains(out, "Removed") || !strings.Contains(out, "issue_tracker.provider") {
			t.Errorf("expected 'Removed issue_tracker.provider ...' confirmation; got:\n%s", out)
		}
	})
}

// TestConfigCheck covers the tree-shaped check output: passing groups
// render as "N/N keys" leaves, failing groups expand to per-key
// children with specific issue details, and the group filter narrows
// the tree to a single branch.
func TestConfigCheck(t *testing.T) {
	t.Run("passing group shows N/N keys leaf", func(t *testing.T) {
		out, err := runConfig(t, "check", "vcs.branch")
		if err != nil {
			t.Fatalf("check: %v\nstdout: %s", err, out)
		}
		// The vcs.branch sub-group's keys all have schema defaults, so
		// it passes. It is also a sub-group, which is what makes it a
		// check that flattened dotted paths reach the filter at all.
		if !strings.Contains(out, "branch") || !strings.Contains(out, "/") || !strings.Contains(out, "keys") {
			t.Errorf("expected 'branch · N/N keys' leaf; got:\n%s", out)
		}
	})

	t.Run("missing required key expands to per-key leaf", func(t *testing.T) {
		// baseConfig sets issue_tracker.provider + base_url but not
		// email or token (both required) — those should each get their
		// own "not set" leaf under the issue_tracker branch.
		out, err := runConfig(t, "check", "issue_tracker")
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		if !strings.Contains(out, "issue_tracker") {
			t.Errorf("expected issue_tracker branch in output; got:\n%s", out)
		}
		if !strings.Contains(out, "not set") {
			t.Errorf("expected 'not set' leaf for missing required key; got:\n%s", out)
		}
	})

	t.Run("invalid option value shows expected list", func(t *testing.T) {
		h := testharness.New(t)
		// baseConfig already declares `ui:`; can't append another
		// block under the same key (duplicate-key YAML error). Write a
		// single composite config with `ui.color` set to a value
		// outside its Options list.
		h.Workspace.WriteConfig(`
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
ui:
  compact_header: true
  color: bogus
`)
		if err := h.Run("config", "check", "ui"); err != nil {
			t.Fatalf("check: %v", err)
		}
		out := h.Stdout()
		if !strings.Contains(out, `"bogus"`) || !strings.Contains(out, "expected:") {
			t.Errorf("expected '\"bogus\" (expected: ...)' leaf; got:\n%s", out)
		}
	})

	t.Run("required key set via BOSUN_* env var counts as set", func(t *testing.T) {
		// baseConfig omits issue_tracker.email + issue_tracker.token
		// (both required) — without env vars they'd be "missing".
		// Setting them via the automatic BOSUN_* names should
		// satisfy the check: ✓ N/N keys, not a ✗ leaf per key.
		t.Setenv("BOSUN_ISSUE_TRACKER_EMAIL", "user@example.com")
		t.Setenv("BOSUN_ISSUE_TRACKER_TOKEN", "tok-from-env")
		out, err := runConfig(t, "check", "issue_tracker")
		if err != nil {
			t.Fatalf("check: %v\nstdout: %s", err, out)
		}
		if strings.Contains(out, "not set") {
			t.Errorf("env-provided required keys should not be 'not set'; got:\n%s", out)
		}
	})

	t.Run("required key set via explicit EnvVar counts as set", func(t *testing.T) {
		// code_host.token's schema declares EnvVar: "GITHUB_TOKEN" —
		// neither the file nor BOSUN_CODE_HOST_TOKEN sets it, but
		// GITHUB_TOKEN should be honored by validateGroup.
		h := testharness.New(t)
		h.Workspace.WriteConfig(`
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
  email: user@example.com
  token: tok
code_host:
  provider: github
  owner: example
`)
		t.Setenv("GITHUB_TOKEN", "tok-from-explicit-env")
		if err := h.Run("config", "check", "code_host"); err != nil {
			t.Fatalf("check: %v", err)
		}
		out := h.Stdout()
		if strings.Contains(out, "not set") {
			t.Errorf("GITHUB_TOKEN should satisfy code_host.token (EnvVar override); got:\n%s", out)
		}
	})

	t.Run("group filter narrows the tree", func(t *testing.T) {
		out, err := runConfig(t, "check", "vcs.branch")
		if err != nil {
			t.Fatalf("check: %v", err)
		}
		// Only the filtered group should appear — other top-level
		// groups like notification / workspace should be absent.
		if !strings.Contains(out, "branch") {
			t.Errorf("expected branch in filtered output; got:\n%s", out)
		}
		if strings.Contains(out, "notification") || strings.Contains(out, "workspace") {
			t.Errorf("filtered tree leaked unrelated groups; got:\n%s", out)
		}
	})
}

// TestConfigGetMasksSecrets locks the machine-format masking: Secret-
// typed keys render masked in every -f dump — including env-derived
// values that injectSchemaDefaults pulls into the settings map — while
// an exact-key raw get stays the deliberate escape hatch for scripts.
func TestConfigGetMasksSecrets(t *testing.T) {
	const fileSecret = "filesecret123"
	const envSecret = "envsecret456"
	const mask = "••••••••"
	secretConfig := `
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
  token: ` + fileSecret + `
`

	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		t.Setenv("GITHUB_TOKEN", envSecret)
		h := testharness.New(t)
		h.Workspace.WriteConfig(secretConfig)
		err := h.Run(append([]string{"config"}, args...)...)
		return h.Stdout(), err
	}

	t.Run("full env dump masks file and env secrets", func(t *testing.T) {
		out, err := run(t, "get", "-f", "env")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if strings.Contains(out, fileSecret) || strings.Contains(out, envSecret) {
			t.Errorf("env dump leaks a secret:\n%s", out)
		}
		if !strings.Contains(out, mask) {
			t.Errorf("env dump should render the mask; got:\n%s", out)
		}
	})

	t.Run("yaml subtree dump masks the token", func(t *testing.T) {
		out, err := run(t, "get", "issue_tracker", "-f", "yaml")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if strings.Contains(out, fileSecret) {
			t.Errorf("yaml subtree leaks the token:\n%s", out)
		}
		if !strings.Contains(out, mask) {
			t.Errorf("yaml subtree should render the mask; got:\n%s", out)
		}
	})

	t.Run("json dump masks the token", func(t *testing.T) {
		out, err := run(t, "get", "issue_tracker", "-f", "json")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if strings.Contains(out, fileSecret) {
			t.Errorf("json dump leaks the token:\n%s", out)
		}
	})

	t.Run("raw exact-key get returns the real value", func(t *testing.T) {
		out, err := run(t, "get", "issue_tracker.token")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got := strings.TrimSpace(out); got != fileSecret {
			t.Errorf("stdout = %q, want the real token (raw exact-key is the escape hatch)", got)
		}
	})
}

// TestConfigCheckUnknownKeys covers the half of `config check` that
// walks the config against the schema rather than the schema against
// the config. It is the only thing that surfaces a key the reshape
// renamed: the new key merely looks unset, which for an optional key is
// silent, so a config still carrying `display.color` would otherwise
// report clean while the setting did nothing.
func TestConfigCheckUnknownKeys(t *testing.T) {
	t.Run("a renamed key is reported", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(`
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
display:
  color: ansi
`)
		if err := h.Run("config", "check"); err != nil {
			t.Fatalf("check: %v", err)
		}
		out := h.Stdout()
		if !strings.Contains(out, "display.color") || !strings.Contains(out, "not in schema") {
			t.Errorf("expected a 'display.color · not in schema' row; got:\n%s", out)
		}
	})

	t.Run("the new home is not reported", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(`
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
  statuses:
    triage: "Triage"
ui:
  color: ansi
services:
  api: api-svc
`)
		if err := h.Run("config", "check"); err != nil {
			t.Fatalf("check: %v", err)
		}
		// ui.color is declared, statuses is map-shaped so a state bosun
		// doesn't model is the user's business, and services is
		// declared map-shaped too — keyed by repository name, which is
		// the central half of the per-repo layer.
		if out := h.Stdout(); strings.Contains(out, "not in schema") {
			t.Errorf("a valid config reported unknown keys:\n%s", out)
		}
	})
}

// TestConfigCheckReachesRepoDescriptors covers the validation-reach
// half of the per-repo layer: `bosun config check` walks every
// repository's committed `.bosun.yaml`, not only the central config.
//
// It matters more than the central walk does. A descriptor is never
// merged into viper — each is read per repository, on demand — so this
// walk is the ONLY thing that can see a typo or a misplaced key in one.
// Without it a repository could carry a broken descriptor indefinitely
// and every command would just quietly fall back to central config.
func TestConfigCheckReachesRepoDescriptors(t *testing.T) {
	// checkWithDescriptor builds a project with one repo carrying the
	// given descriptor, runs check, and returns the harness plus what
	// the tree printed.
	checkWithDescriptor := func(t *testing.T, descriptor string) (*testharness.Harness, string) {
		t.Helper()
		h := testharness.New(t)
		h.Workspace.WriteConfig(`
workspace:
  repositories:
    - "repos/*"
  root: trees
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
`)
		repo := h.Workspace.AddRepo("api")
		path := filepath.Join(repo.Path, config.RepoConfigFile)
		if err := os.WriteFile(path, []byte(descriptor), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := h.Run("config", "check"); err != nil {
			t.Fatalf("check: %v", err)
		}
		return h, h.Stdout()
	}

	t.Run("a valid descriptor is counted, not reported", func(t *testing.T) {
		h, out := checkWithDescriptor(t, "services: [billing]\npull_request:\n  base: develop\n")
		if strings.Contains(out, "misplaced keys") {
			t.Errorf("a valid descriptor was reported as misplaced:\n%s", out)
		}

		// The count is what separates "descriptors are clean" from "no
		// descriptor was ever read" — silence alone means both. It
		// rides on the summary card, so it is read from the Reporter
		// rather than from the tree above it.
		summaries := h.Reporter.OfKind(ui.CaptureSummary)
		if len(summaries) != 1 {
			t.Fatalf("summary events = %d, want 1", len(summaries))
		}
		if !strings.Contains(summaries[0].Label, "1 repo descriptor") {
			t.Errorf("summary = %q, want it to report reading the descriptor", summaries[0].Label)
		}
	})

	t.Run("a committed credential is reported", func(t *testing.T) {
		_, raw := checkWithDescriptor(t, "code_host:\n  token: ghp_secret\n")
		out := ansi.Strip(raw)
		if !strings.Contains(out, "misplaced keys") || !strings.Contains(out, "code_host.token") {
			t.Errorf("a committed token was not reported:\n%s", out)
		}
		if !strings.Contains(out, "api/"+config.RepoConfigFile) {
			t.Errorf("the report does not name the offending file:\n%s", out)
		}
		// And the value never appears, here or anywhere else.
		if strings.Contains(out, "ghp_secret") {
			t.Errorf("check printed the secret it was reporting:\n%s", out)
		}
	})

	t.Run("an unknown descriptor key is reported", func(t *testing.T) {
		_, raw := checkWithDescriptor(t, "pull_reqeust:\n  base: main\n")
		out := ansi.Strip(raw)
		if !strings.Contains(out, "pull_reqeust.base") || !strings.Contains(out, "not in schema") {
			t.Errorf("a typo'd descriptor key was not reported:\n%s", out)
		}
	})
}

// TestConfigShowNestedGroups covers the tree `config show` renders for a
// config whose real shape is nested. Sub-groups have to survive as
// sub-trees, and a list-valued key inside one has to render like a list
// rather than leaking viper's "[a b]" formatting.
func TestConfigShowNestedGroups(t *testing.T) {
	h := testharness.New(t)
	h.Workspace.WriteConfig(`
workspace:
  repositories:
    - "repos/*"
    - "vendor/*"
  root: trees
notification:
  channels:
    review: bb-prs
`)
	if err := h.Run("config", "show"); err != nil {
		t.Fatalf("show: %v", err)
	}
	out := ansi.Strip(h.Stdout())

	for _, want := range []string{"channels", "review", "bb-prs", "repositories"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree is missing %q; got:\n%s", want, out)
		}
	}
	// formatValue's job: the same unwrapping buildLeafNode has always
	// done for top-level lists, now that a list lives inside a group.
	if !strings.Contains(out, "repos/*, vendor/*") {
		t.Errorf("list rendered unformatted; got:\n%s", out)
	}
	if strings.Contains(out, "[repos/*") {
		t.Errorf("viper's raw slice formatting leaked into the tree; got:\n%s", out)
	}
}

// TestConfigCheckFilterReachesSubGroups pins that the group filter is a
// prefix, not an equality test. The schema nests, so `check preview`
// under equality validated the two keys sitting directly in the block
// and reported a clean pass while up/down and their inputs went
// unchecked — and `check vcs`, a block with no keys of its own,
// answered "0 checks" for a fully configured branch template.
func TestConfigCheckFilterReachesSubGroups(t *testing.T) {
	run := func(t *testing.T, group string) string {
		t.Helper()
		h := testharness.New(t)
		h.Workspace.WriteConfig(`
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
preview:
  url_template: "https://{{.Name}}.example.test"
  up:
    workflow: acme/infra/.github/workflows/up.yml
    inputs:
      name: env-name
`)
		if err := h.Run("config", "check", group); err != nil {
			t.Fatalf("check %s: %v", group, err)
		}
		return ansi.Strip(h.Stdout())
	}

	t.Run("a block reaches its sub-groups", func(t *testing.T) {
		out := run(t, "preview")
		for _, want := range []string{"preview.up", "preview.up.inputs"} {
			if !strings.Contains(out, want) {
				t.Errorf("check preview missed %q; got:\n%s", want, out)
			}
		}
		// Still scoped: an unrelated block must not appear.
		if strings.Contains(out, "issue_tracker") {
			t.Errorf("filtered tree leaked an unrelated group; got:\n%s", out)
		}
	})

	t.Run("a keyless block reaches the keys beneath it", func(t *testing.T) {
		out := run(t, "vcs")
		if !strings.Contains(out, "vcs.branch") {
			t.Errorf("check vcs validated nothing; got:\n%s", out)
		}
	})
}
