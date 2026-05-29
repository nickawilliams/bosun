package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// startConfig is the minimum project config required for `bosun start`:
// repository glob, workspace root, and the in_progress status mapping.
const startConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  statuses:
    in_progress: "In Progress"
`

// TestStart exercises the bosun start command end-to-end through the
// test harness. Each sub-test seeds fakes + config, runs the command,
// and asserts on the resulting branch/worktree/status state.
//
// Group sub-tests by the dimension they exercise (issue resolution,
// repository selection, plan confirmation, idempotency, errors,
// branch naming) so the t.Run hierarchy doubles as the scenario tree
// for this command.
func TestStart(t *testing.T) {
	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-1", Title: "Add provider lookup", Type: "Story",
		})

		if err := h.Run("start", "--issue", "EX-1", "--slug", "provider-lookup", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		branch := "story/EX-1_provider-lookup"
		if !api.HasBranch(branch) {
			t.Errorf("expected branch %q in api repo; got branches missing", branch)
		}

		worktree := h.WorktreePath(branch, "api")
		if !api.WorktreeExists(worktree) {
			t.Errorf("expected worktree at %q", worktree)
		}

		got, _ := h.Tracker.Issue("EX-1")
		if got.Status != "In Progress" {
			t.Errorf("issue status = %q, want %q", got.Status, "In Progress")
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Add audit log", Type: "Story",
		})

		// Dry-run returns ErrCancelled (the plan was rendered but no
		// apply happened). That's expected — we assert no mutations.
		err := h.Run("start", "--issue", "EX-2", "--slug", "audit-log", "--dry-run")
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		branch := "story/EX-2_audit-log"
		if api.HasBranch(branch) {
			t.Errorf("dry-run created branch %q; should not have mutated", branch)
		}

		got, _ := h.Tracker.Issue("EX-2")
		if got.Status == "In Progress" {
			t.Errorf("dry-run set issue status to %q; should not have mutated", got.Status)
		}
	})

	t.Run("repository_selection/filtered_by_flag", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		web := h.Workspace.AddRepo("web")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-3", Title: "Wire upload", Type: "Story",
		})

		if err := h.Run(
			"start", "--issue", "EX-3", "--slug", "wire-upload",
			"--repository", "api", "--yes",
		); err != nil {
			t.Fatalf("start: %v", err)
		}

		branch := "story/EX-3_wire-upload"
		if !api.HasBranch(branch) {
			t.Errorf("expected branch %q in api repo", branch)
		}
		if web.HasBranch(branch) {
			t.Errorf("did not expect branch %q in web repo (filtered out)", branch)
		}
	})

	t.Run("idempotency/branch_already_exists", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-4", Title: "Add retry policy", Type: "Story",
		})

		branch := "story/EX-4_retry-policy"

		// First run creates the branch + worktree + sets status.
		if err := h.Run("start", "--issue", "EX-4", "--slug", "retry-policy", "--yes"); err != nil {
			t.Fatalf("first run: %v", err)
		}
		if !api.HasBranch(branch) {
			t.Fatalf("first run did not create branch %q", branch)
		}

		// Second run should detect everything already exists and apply
		// nothing — no error, no duplicate work.
		if err := h.Run("start", "--issue", "EX-4", "--slug", "retry-policy", "--yes"); err != nil {
			t.Fatalf("second run: %v", err)
		}

		// Tracker should have seen status updates only when needed.
		// The exact call count isn't load-bearing — what matters is
		// the issue is still in_progress and no error fired.
		got, _ := h.Tracker.Issue("EX-4")
		if got.Status != "In Progress" {
			t.Errorf("issue status = %q, want %q", got.Status, "In Progress")
		}
	})

	t.Run("branch_naming/derived_from_summary", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-5", Title: "Investigate slow endpoint", Type: "Bug",
		})

		// No --slug means start enters the interactive slug prompt with
		// the slugified title as its placeholder default. Pressing enter
		// with no typed content accepts that default — which is exactly
		// what this scenario is checking.
		h.Type("\r")

		if err := h.Run("start", "--issue", "EX-5", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		want := "bug/EX-5_investigate-slow-endpoint"
		if !api.HasBranch(want) {
			t.Errorf("expected branch %q derived from issue title; got missing", want)
		}
	})

	t.Run("repository_selection/multi_repo_interactive_select", func(t *testing.T) {
		// Two repos, no --repository filter: start enters the
		// interactive multi-select. Space toggles the focused option,
		// down arrow moves focus, Enter submits. Sequence below toggles
		// both api and web, then submits, then confirms the plan.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		web := h.Workspace.AddRepo("web")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-11", Title: "Wire both", Type: "Story",
		})

		// " " toggles first option; "\x1b[B" is down-arrow; second " "
		// toggles next; "\r" submits the multi-select; --yes skips the
		// plan confirmation that follows.
		h.Type(" \x1b[B \r")

		if err := h.Run("start", "--issue", "EX-11", "--slug", "both", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		branch := "story/EX-11_both"
		if !api.HasBranch(branch) {
			t.Errorf("expected branch %q in api", branch)
		}
		if !web.HasBranch(branch) {
			t.Errorf("expected branch %q in web", branch)
		}
	})

	t.Run("plan_confirmation/confirmed_applies", func(t *testing.T) {
		// Without --yes the plan confirmation gate runs as a huh form.
		// Affirmative is "Apply", negative is "Cancel"; with an empty
		// stdin and the form's default at "Apply" position, pressing
		// Enter accepts. \r is what bubbletea's input parser sees for
		// the Enter key in test mode.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-7", Title: "Add export endpoint", Type: "Story",
		})
		// huh.Confirm starts focused on the Negative button (Value defaults
		// to false). 'y' selects and submits the affirmative.
		h.Type("y")

		if err := h.Run("start", "--issue", "EX-7", "--slug", "export"); err != nil {
			t.Fatalf("start: %v", err)
		}

		if !api.HasBranch("story/EX-7_export") {
			t.Errorf("expected branch story/EX-7_export to exist")
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// "n" selects the negative (Cancel) button on huh.Confirm.
		// The command returns ErrCancelled and makes no mutations.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-8", Title: "Add report builder", Type: "Story",
		})
		h.Type("n")

		err := h.Run("start", "--issue", "EX-8", "--slug", "report")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want contains \"cancelled\"", err)
		}

		if api.HasBranch("story/EX-8_report") {
			t.Errorf("cancelled run created branch; should not have")
		}
		got, _ := h.Tracker.Issue("EX-8")
		if got.Status == "In Progress" {
			t.Errorf("cancelled run set status; should not have")
		}
	})

	t.Run("idempotency/status_already_in_progress", func(t *testing.T) {
		// Issue is already in_progress: branch creation still runs (new
		// branch), but the status action should be a no-op. The tracker
		// records SetStatus calls — we verify it isn't called.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-9", Title: "Tweak", Type: "Story", Status: "In Progress",
		})

		if err := h.Run("start", "--issue", "EX-9", "--slug", "tweak", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		if slices.Contains(h.Tracker.Calls(), "SetStatus") {
			t.Errorf("SetStatus called when issue already in_progress; calls=%v", h.Tracker.Calls())
		}
	})

	t.Run("issue_resolution/from_env", func(t *testing.T) {
		// BOSUN_ISSUE is the env var binding for --issue. Used by direnv
		// setups so each workspace dir auto-provides its issue key.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-10", Title: "Env-resolved", Type: "Story",
		})
		t.Setenv("BOSUN_ISSUE", "EX-10")

		if err := h.Run("start", "--slug", "env-resolved", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		if !api.HasBranch("story/EX-10_env-resolved") {
			t.Errorf("expected branch story/EX-10_env-resolved")
		}
	})

	t.Run("errors/issue_not_found", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		h.Workspace.AddRepo("api")
		// Don't seed EX-99 — the tracker should return not-found.

		err := h.Run("start", "--issue", "EX-99", "--slug", "missing", "--yes")
		if err == nil {
			t.Fatalf("expected error for missing issue; got nil")
		}
		if !strings.Contains(err.Error(), "EX-99") {
			t.Errorf("error %q does not mention the missing key", err)
		}
	})

	t.Run("absolute_worktree_path", func(t *testing.T) {
		// Sanity: worktree ends up at {workspace.root}/{branch}/{repo}
		// resolved relative to the project root. Exposed as a separate
		// test so the layout assumption is documented.
		h := testharness.New(t)
		h.Workspace.WriteConfig(startConfig)
		h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-6", Title: "X", Type: "Story",
		})

		if err := h.Run("start", "--issue", "EX-6", "--slug", "x", "--yes"); err != nil {
			t.Fatalf("start: %v", err)
		}

		expect := filepath.Join(h.Workspace.Dir, "workspaces", "story/EX-6_x", "api")
		if _, err := os.Stat(expect); err != nil {
			t.Errorf("worktree path %q missing: %v", expect, err)
		}
	})
}
