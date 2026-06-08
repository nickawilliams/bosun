package cli_test

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/testharness"
)

const baseConfig = `
issue_tracker:
  provider: jira
  base_url: https://example.atlassian.net
display:
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
		// only `issue_tracker` and `display`, so the global-only view
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
