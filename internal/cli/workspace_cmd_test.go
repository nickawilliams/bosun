package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// workspaceConfig is the minimum project config for the workspace
// commands: repository glob + workspace root.
const workspaceConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
`

// TestWorkspaceReposRm exercises `workspace repos rm` end-to-end: the
// repo-level removal deletes the worktree AND the branch, unmatched
// repo names error instead of silently narrowing, and the plan
// confirmation gates non-interactive runs (--force alone must not
// approve).
func TestWorkspaceReposRm(t *testing.T) {
	t.Run("removes worktree and branch", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-a", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		wt := h.WorktreePath("ws-a", "api")
		if !api.WorktreeExists(wt) {
			t.Fatalf("fixture: worktree missing at %q", wt)
		}

		if err := h.Run("workspace", "repos", "rm", "api", "--workspace", "ws-a", "--approve"); err != nil {
			t.Fatalf("rm: %v", err)
		}
		if api.WorktreeExists(wt) {
			t.Errorf("worktree still present at %q", wt)
		}
		if api.HasBranch("ws-a") {
			t.Errorf("branch ws-a survived the removal")
		}
	})

	t.Run("unmatched repo name errors", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-b", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}

		err := h.Run("workspace", "repos", "rm", "api", "typo", "--workspace", "ws-b", "--approve")
		if err == nil || !strings.Contains(err.Error(), "typo") {
			t.Fatalf("err = %v, want the unmatched name called out", err)
		}
		// Nothing was removed: the error fired before any destruction.
		if !api.WorktreeExists(h.WorktreePath("ws-b", "api")) {
			t.Errorf("worktree destroyed despite the unmatched-name error")
		}
	})

	t.Run("declining the confirmation preserves the workspace", func(t *testing.T) {
		// Without --approve the plan confirmation form runs (harness
		// stdin reads as interactive); "n" picks Cancel. This also
		// locks the --force/--approve split at the command level:
		// nothing but an approval answers the plan.
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-c", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		h.Type("n")

		err := h.Run("workspace", "repos", "rm", "api", "--workspace", "ws-c")
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want cancellation", err)
		}
		if !api.WorktreeExists(h.WorktreePath("ws-c", "api")) {
			t.Errorf("worktree destroyed despite the declined confirmation")
		}
		if !api.HasBranch("ws-c") {
			t.Errorf("branch destroyed despite the declined confirmation")
		}
	})
}

// TestWorkspaceReposAdd exercises `workspace repos add`.
func TestWorkspaceReposAdd(t *testing.T) {
	t.Run("adds repo to existing workspace", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")
		web := h.Workspace.AddRepo("web")

		if err := h.Run("workspace", "create", "ws-e", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		if !api.WorktreeExists(h.WorktreePath("ws-e", "api")) {
			t.Fatalf("fixture: api worktree missing")
		}

		if err := h.Run("workspace", "repos", "add", "web", "--workspace", "ws-e"); err != nil {
			t.Fatalf("repos add: %v", err)
		}
		if !web.WorktreeExists(h.WorktreePath("ws-e", "web")) {
			t.Errorf("web worktree not created after repos add")
		}
	})
}

// TestWorkspaceRepos exercises the `workspace repos` parent command
// (interactive path). The form-driven add/remove flow is untestable
// via the harness (Interactive() always returns true for the injected
// reader, so the form blocks — see ui.Interactive). The test here
// covers the pre-form guard: unknown subcommand names (including bare
// repo names passed by mistake) are rejected by Cobra before the RunE
// runs, since the parent only accepts the registered subcommands.
func TestWorkspaceRepos(t *testing.T) {
	t.Run("rejects unknown subcommands", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-g", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}

		err := h.Run("workspace", "repos", "api", "--workspace", "ws-g")
		if err == nil {
			t.Fatal("expected error for unknown subcommand, got nil")
		}
		if !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("err = %q, want unknown command error", err)
		}
	})
}

// dirty makes the worktree at path report as modified by dropping an
// untracked file into it — `git status --porcelain` is non-empty, which
// is what workspace.Status reads for RepositoryStatus.Dirty.
func dirty(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("dirtying %s: %v", path, err)
	}
}

// TestWorkspaceRemovalReadiness locks the pre-plan safety gate shared by
// the three destructive commands: a repo with uncommitted changes stops
// the run BEFORE the plan card is shown, so --approve alone can't get
// past it. The gate is what makes the plan trustworthy — the plan is
// only ever displayed for a removal that's already been vetted.
func TestWorkspaceRemovalReadiness(t *testing.T) {
	t.Run("repos rm blocks on a dirty repo", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-h", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		wt := h.WorktreePath("ws-h", "api")
		dirty(t, wt)

		err := h.Run("workspace", "repos", "rm", "api", "--workspace", "ws-h", "--approve")
		if err == nil {
			t.Fatal("expected the readiness gate to block, got nil")
		}
		if !strings.Contains(err.Error(), "--force") {
			t.Errorf("err = %q, want the --force override called out", err)
		}
		// The gate fired before anything was destroyed.
		if !api.WorktreeExists(wt) {
			t.Errorf("worktree destroyed despite the readiness block")
		}
		if !api.HasBranch("ws-h") {
			t.Errorf("branch destroyed despite the readiness block")
		}
	})

	t.Run("delete blocks on a dirty repo", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-i", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		wt := h.WorktreePath("ws-i", "api")
		dirty(t, wt)

		err := h.Run("workspace", "delete", "ws-i", "--approve")
		if err == nil {
			t.Fatal("expected the readiness gate to block, got nil")
		}
		if !strings.Contains(err.Error(), "--force") {
			t.Errorf("err = %q, want the --force override called out", err)
		}
		if !api.WorktreeExists(wt) {
			t.Errorf("worktree destroyed despite the readiness block")
		}
	})

	t.Run("--force soft-confirms and cancelling preserves the workspace", func(t *testing.T) {
		// With --force the gate downgrades to a Dialog rather than a
		// hard block. Answering "n" cancels — --force buys a prompt,
		// not an unconditional removal.
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-j", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		wt := h.WorktreePath("ws-j", "api")
		dirty(t, wt)
		h.Type("n")

		err := h.Run("workspace", "delete", "ws-j", "--force", "--approve")
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want cancellation", err)
		}
		if !api.WorktreeExists(wt) {
			t.Errorf("worktree destroyed despite the declined confirmation")
		}
	})

	t.Run("--force with confirmation removes the dirty repo", func(t *testing.T) {
		h := testharness.New(t)
		h.Workspace.WriteConfig(workspaceConfig)
		api := h.Workspace.AddRepo("api")

		if err := h.Run("workspace", "create", "ws-k", "api"); err != nil {
			t.Fatalf("create: %v", err)
		}
		wt := h.WorktreePath("ws-k", "api")
		dirty(t, wt)
		h.Type("y")

		if err := h.Run("workspace", "delete", "ws-k", "--force", "--approve"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if api.WorktreeExists(wt) {
			t.Errorf("worktree survived the confirmed force delete")
		}
	})
}

// TestWorkspaceDelete exercises `workspace delete`: the whole
// workspace goes — worktree, branch, and the workspace directory.
func TestWorkspaceDelete(t *testing.T) {
	h := testharness.New(t)
	h.Workspace.WriteConfig(workspaceConfig)
	api := h.Workspace.AddRepo("api")

	if err := h.Run("workspace", "create", "ws-d", "api"); err != nil {
		t.Fatalf("create: %v", err)
	}
	wt := h.WorktreePath("ws-d", "api")

	if err := h.Run("workspace", "delete", "ws-d", "--approve"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if api.WorktreeExists(wt) {
		t.Errorf("worktree still present at %q", wt)
	}
	if api.HasBranch("ws-d") {
		t.Errorf("branch ws-d survived the delete")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("workspace directory still present: %v", err)
	}
}

// TestStartHeterogeneousBranchReuse locks the actualBranch pattern:
// re-running start on a workspace whose worktree was manually
// switched to a different branch must key the per-repo actions off
// the worktree's REAL head — not recreate a dangling sibling branch
// under the workspace's name.
func TestStartHeterogeneousBranchReuse(t *testing.T) {
	h := testharness.New(t)
	h.Workspace.WriteConfig(`
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  statuses:
    in_progress: "In Progress"
`)
	api := h.Workspace.AddRepo("api")
	h.Tracker.SeedIssue(issue.Issue{Key: "EX-9", Title: "Reuse me", Type: "Story"})

	if err := h.Run("start", "--issue", "EX-9", "--slug", "reuse-me", "--approve"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	original := "story/EX-9_reuse-me"
	wt := h.WorktreePath(original, "api")

	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s\n%s", args, err, out)
		}
	}
	// The user switches the worktree to a custom branch and drops the
	// original — the workspace now runs on a branch that doesn't match
	// its name.
	gitRun("-C", wt, "checkout", "-b", "custom-work")
	gitRun("-C", wt, "commit", "--allow-empty", "-m", "wip")
	gitRun("-C", api.Path, "branch", "-D", original)

	if err := h.Run("start", "--issue", "EX-9", "--approve"); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if api.HasBranch(original) {
		t.Errorf("start recreated the dangling %q branch instead of keying off the worktree's real head", original)
	}
	if !api.HasBranch("custom-work") {
		t.Errorf("custom branch disappeared")
	}
	if !api.WorktreeExists(wt) {
		t.Errorf("worktree missing after reuse")
	}
}
