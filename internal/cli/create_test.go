package cli_test

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/testharness"
)

// createConfig is the minimum project config required for `bosun
// create`: the tracker project the issue is created under.
const createConfig = `
issue_tracker:
  project: "EX"
`

// TestCreate exercises the bosun create command end-to-end through the
// test harness. Each sub-test seeds config, pre-fills any interactive
// input, runs the command, and asserts on the tracker's created-issue
// state.
//
// Group sub-tests by the dimension they exercise (fields from flags,
// interactive form, issue type, plan confirmation, errors) so the
// t.Run hierarchy doubles as the scenario tree for this command.
func TestCreate(t *testing.T) {
	t.Run("fields_from_flags/all_provided_no_prompt", func(t *testing.T) {
		// All field flags provided → no issue-details form. The plan
		// confirmation gate still prompts (no --approve); "y" applies.
		// Success with only that single keypress in stdin proves no
		// field prompts appeared.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)
		h.Type("y")

		if err := h.Run(
			"create", "--title", "Add rate limiter",
			"--description", "Token bucket per client", "--type", "story",
		); err != nil {
			t.Fatalf("create: %v", err)
		}

		issues := h.Tracker.Issues()
		if len(issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(issues))
		}
		got := issues[0]
		if got.Key != "EX-1" {
			t.Errorf("key = %q, want %q", got.Key, "EX-1")
		}
		if got.Title != "Add rate limiter" {
			t.Errorf("title = %q, want %q", got.Title, "Add rate limiter")
		}
		if got.Type != "story" {
			t.Errorf("type = %q, want %q", got.Type, "story")
		}
		if got.Status != "Open" {
			t.Errorf("status = %q, want %q", got.Status, "Open")
		}
	})

	t.Run("fields_from_flags/title_only_others_prompted", func(t *testing.T) {
		// --title suppresses only the title field; description
		// (textarea) and type (select) still prompt. Enter submits the
		// textarea (alt+enter/ctrl+j insert newlines), enter accepts
		// the select's pre-selected default. One Type call per field —
		// see Harness.Type.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)
		h.Type("Bucket size is configurable\r")
		h.Type("\r")

		if err := h.Run("create", "--title", "Add rate limiter", "--approve"); err != nil {
			t.Fatalf("create: %v", err)
		}

		issues := h.Tracker.Issues()
		if len(issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(issues))
		}
		if issues[0].Title != "Add rate limiter" {
			t.Errorf("title = %q, want %q", issues[0].Title, "Add rate limiter")
		}
		if issues[0].Type != "story" {
			t.Errorf("type = %q, want %q (select default)", issues[0].Type, "story")
		}
	})

	t.Run("interactive/full_form_submission", func(t *testing.T) {
		// No field flags → title input, description textarea, and type
		// select all prompt, in that order. Enter walks each field;
		// one Type call per field — see Harness.Type.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)
		h.Type("Add audit log\r")
		h.Type("Persist admin actions\r")
		h.Type("\r")

		if err := h.Run("create", "--approve"); err != nil {
			t.Fatalf("create: %v", err)
		}

		issues := h.Tracker.Issues()
		if len(issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(issues))
		}
		if issues[0].Title != "Add audit log" {
			t.Errorf("title = %q, want %q", issues[0].Title, "Add audit log")
		}
	})

	t.Run("interactive/cancelled_returns_err_cancelled", func(t *testing.T) {
		// Ctrl+c during the issue-details form aborts the run:
		// huh.ErrUserAborted → ErrCancelled, and nothing is created.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)
		h.Type("\x03")

		err := h.Run("create", "--approve")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want contains \"cancelled\"", err)
		}

		if n := len(h.Tracker.Issues()); n != 0 {
			t.Errorf("cancelled run created %d issue(s); want 0", n)
		}
	})

	t.Run("issue_type/story_default", func(t *testing.T) {
		// --type defaults to story, but the interactive gate keys on
		// Changed("type") — omitting the flag still prompts the select
		// with story pre-selected. Enter accepts it; the default must
		// land on the created issue.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)
		h.Type("\r")

		if err := h.Run(
			"create", "--title", "Add export", "--description", "CSV export", "--approve",
		); err != nil {
			t.Fatalf("create: %v", err)
		}

		issues := h.Tracker.Issues()
		if len(issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(issues))
		}
		if issues[0].Type != "story" {
			t.Errorf("type = %q, want %q", issues[0].Type, "story")
		}
	})

	t.Run("issue_type/bug_from_flag", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)

		if err := h.Run(
			"create", "--title", "Fix crash on empty input",
			"--description", "Nil deref in parser", "--type", "bug", "--approve",
		); err != nil {
			t.Fatalf("create: %v", err)
		}

		issues := h.Tracker.Issues()
		if len(issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(issues))
		}
		if issues[0].Type != "bug" {
			t.Errorf("type = %q, want %q", issues[0].Type, "bug")
		}
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// --approve with an empty stdin: nothing prompts at all —
		// neither the field form (all flags given) nor the plan gate.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)

		if err := h.Run(
			"create", "--title", "Add webhooks",
			"--description", "Outbound events", "--type", "story", "--approve",
		); err != nil {
			t.Fatalf("create: %v", err)
		}

		if n := len(h.Tracker.Issues()); n != 1 {
			t.Fatalf("issues = %d, want 1", n)
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// Dry-run renders the plan but never applies — surfaces as a
		// "cancelled" error (matching start's behavior) with no
		// tracker mutations.
		h := testharness.New(t)
		h.Workspace.WriteConfig(createConfig)

		err := h.Run(
			"create", "--title", "Add webhooks",
			"--description", "Outbound events", "--type", "story", "--dry-run",
		)
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		if n := len(h.Tracker.Issues()); n != 0 {
			t.Errorf("dry-run created %d issue(s); want 0", n)
		}
		for _, call := range h.Tracker.Calls() {
			if call == "CreateIssue" {
				t.Errorf("dry-run called CreateIssue; calls=%v", h.Tracker.Calls())
			}
		}
	})

	t.Run("errors/missing_project_config", func(t *testing.T) {
		// No issue_tracker.project in config. The check runs after the
		// title guard and the interactive form, so all field flags are
		// supplied to reach it.
		h := testharness.New(t)
		h.Workspace.WriteConfig(`workspace: {}`)

		err := h.Run(
			"create", "--title", "Orphan issue",
			"--description", "No project", "--type", "story", "--approve",
		)
		if err == nil {
			t.Fatalf("expected missing-project error; got nil")
		}
		if !strings.Contains(err.Error(), "issue_tracker.project") {
			t.Errorf("error %q does not mention issue_tracker.project", err)
		}

		if n := len(h.Tracker.Issues()); n != 0 {
			t.Errorf("created %d issue(s) without project config; want 0", n)
		}
	})
}
