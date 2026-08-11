package cli_test

// End-to-end scenarios for `bosun cleanup` through the test harness.
// The pure safety-matrix classification is unit-tested in
// cleanup_readiness_test.go (package cli); this file exercises the full
// command path: workspace resolution, the readiness gate, the preview
// teardown row, plan confirmation, and the real git worktree/branch
// destruction that follows.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// cleanupConfig is the minimum project config for `bosun cleanup`:
// repositories + workspace root (so the worktrees resolve), and the
// status mappings the readiness probe reads the issue's lifecycle
// position from.
const cleanupConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    done: "Done"
`

const cleanupBranch = "story/EX-1_feature"

// startCleanupWorkspace builds a harness with the named repos and a
// started workspace on cleanupBranch — the baseline every cleanup
// scenario tears down. The preview fake is installed because cleanup
// reaches for a provider unconditionally (the teardown row is the
// first entry in its plan).
func startCleanupWorkspace(t *testing.T, repoNames ...string) (*testharness.Harness, []*testharness.Repo) {
	t.Helper()
	h := testharness.New(t)
	h.InstallPreview()
	h.Workspace.WriteConfig(cleanupConfig)

	repos := make([]*testharness.Repo, 0, len(repoNames))
	for _, name := range repoNames {
		repos = append(repos, h.Workspace.AddRepo(name))
	}
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story",
	})
	// --repository names every repo explicitly: with more than one
	// configured, start would otherwise open its interactive
	// multi-select, and the keys for it aren't what these scenarios are
	// about.
	if err := h.Run(
		"start", "--issue", "EX-1", "--slug", "feature",
		"--repository", strings.Join(repoNames, ","), "--approve",
	); err != nil {
		t.Fatalf("start: %v", err)
	}
	return h, repos
}

// markMerged puts a repo in the state cleanup considers fully safe to
// destroy: the issue in a done-like status and a merged PR whose head
// commit is exactly what the worktree still has checked out. Anything
// short of this trips a readiness finding — which is the point of the
// scenarios that deliberately skip it.
func markMerged(t *testing.T, h *testharness.Harness, r *testharness.Repo) {
	t.Helper()
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story", Status: "Done",
	})
	wt := h.WorktreePath(cleanupBranch, r.Name)
	sha := testharness.Git(t, wt, "rev-parse", "HEAD")
	h.Host.SeedPR(r.Owner, r.Name, cleanupBranch, code.PullRequest{
		Number: 7, State: "merged", HeadSHA: sha,
	})
}

// commitUnmergedWork adds a commit on the workspace branch and pushes
// it, leaving work that exists on the remote branch but not in base
// and not behind any PR — the "unmerged work" BLOCK.
func commitUnmergedWork(t *testing.T, h *testharness.Harness, r *testharness.Repo) {
	t.Helper()
	wt := h.WorktreePath(cleanupBranch, r.Name)
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	testharness.Git(t, wt, "add", "feature.txt")
	testharness.Git(t, wt, "commit", "-m", "add feature")
	testharness.Git(t, wt, "push", "origin", cleanupBranch)
}

// runCleanup invokes the command against the started workspace with
// any extra flags appended.
func runCleanup(h *testharness.Harness, extra ...string) error {
	args := append([]string{
		"cleanup", "--workspace", cleanupBranch, "--issue", "EX-1",
	}, extra...)
	return h.Run(args...)
}

// assertWorkspaceGone fails unless every trace of the workspace is
// gone: worktree deregistered, local branch deleted, remote branch
// deleted, and the workspace directory removed.
func assertWorkspaceGone(t *testing.T, h *testharness.Harness, r *testharness.Repo) {
	t.Helper()
	if r.WorktreeExists(h.WorktreePath(cleanupBranch, r.Name)) {
		t.Errorf("%s: worktree still registered", r.Name)
	}
	if r.HasBranch(cleanupBranch) {
		t.Errorf("%s: local branch %s survived", r.Name, cleanupBranch)
	}
	if hasRemoteBranch(t, r, cleanupBranch) {
		t.Errorf("%s: remote branch %s survived", r.Name, cleanupBranch)
	}
	if _, err := os.Stat(workspaceDir(h)); !os.IsNotExist(err) {
		t.Errorf("workspace directory survived (stat err = %v)", err)
	}
}

// assertWorkspaceIntact fails unless the workspace is exactly as
// cleanup found it — the assertion every gate/cancel scenario makes.
func assertWorkspaceIntact(t *testing.T, h *testharness.Harness, r *testharness.Repo) {
	t.Helper()
	if !r.WorktreeExists(h.WorktreePath(cleanupBranch, r.Name)) {
		t.Errorf("%s: worktree was removed", r.Name)
	}
	if !r.HasBranch(cleanupBranch) {
		t.Errorf("%s: local branch %s was deleted", r.Name, cleanupBranch)
	}
	if _, err := os.Stat(workspaceDir(h)); err != nil {
		t.Errorf("workspace directory removed: %v", err)
	}
}

// workspaceDir is the on-disk path of the started workspace.
func workspaceDir(h *testharness.Harness) string {
	return filepath.Dir(h.WorktreePath(cleanupBranch, "any"))
}

// hasRemoteBranch reports whether branch still exists on the repo's
// bare origin — DeleteBranch pushes a delete there, and only checking
// the local ref would miss a half-done teardown.
func hasRemoteBranch(t *testing.T, r *testharness.Repo, branch string) bool {
	t.Helper()
	out := testharness.Git(t, r.RemotePath, "for-each-ref", "--format=%(refname)", "refs/heads/"+branch)
	return out != ""
}

// TestCleanup exercises the bosun cleanup command end-to-end through
// the test harness. Each sub-test starts a workspace, seeds the git +
// fake state that puts the readiness matrix in a particular position,
// runs cleanup, and asserts on what survived.
//
// The tree adapts the planned scenarios to the command as built.
// Cleanup takes no --repository or --all flag: it is scoped to exactly
// one workspace's worktrees, so "filter by repository" and "process
// every workspace" have no implementation to exercise; the scoping
// that does exist (--workspace picks which workspace dies, and every
// repo in it goes) is covered by filter/ instead. Unmerged and dirty
// worktrees BLOCK with an error rather than being skipped with a
// message — cleanup is all-or-nothing per workspace. And cleanup makes
// no tracker status transition, so the harness README's SetStatusErr
// apply-failure case has no seam here; errors/ covers the failure
// modes cleanup actually has instead.
func TestCleanup(t *testing.T) {
	t.Run("merged_branch/removes_branch_and_worktree", func(t *testing.T) {
		// The whole safety matrix reads SAFE: done-like issue, merged
		// PR whose head is what's checked out. No readiness gate, no
		// findings — cleanup destroys the worktree, both ends of the
		// branch, and the workspace directory.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
	})

	t.Run("merged_branch/removes_every_repo_in_workspace", func(t *testing.T) {
		// Cleanup is workspace-scoped, not repo-scoped: one run tears
		// down every repo the workspace spans.
		h, repos := startCleanupWorkspace(t, "api", "web")
		for _, r := range repos {
			markMerged(t, h, r)
		}

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		for _, r := range repos {
			assertWorkspaceGone(t, h, r)
		}
	})

	t.Run("unmerged_branch/blocks_without_force", func(t *testing.T) {
		// Commits are pushed to the branch but aren't in base and no PR
		// records them. That's the unmerged-work BLOCK: cleanup refuses
		// with an actionable error and nothing is destroyed.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, cleanupBranch, code.PullRequest{})
		commitUnmergedWork(t, h, api)

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the unmerged-work block; got nil")
		}
		if !strings.Contains(err.Error(), "blocking findings") {
			t.Errorf("error = %v, want the readiness block", err)
		}

		assertWorkspaceIntact(t, h, api)
		// The point of the block: the commit that exists nowhere but
		// this branch is still reachable from it.
		wt := h.WorktreePath(cleanupBranch, api.Name)
		if _, err := os.Stat(filepath.Join(wt, "feature.txt")); err != nil {
			t.Errorf("unmerged work was destroyed: %v", err)
		}
	})

	t.Run("dirty_worktree/blocks_to_protect_uncommitted_changes", func(t *testing.T) {
		// Uncommitted changes in the worktree BLOCK even though the PR
		// merged cleanly — `git worktree remove` would discard them.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		wt := h.WorktreePath(cleanupBranch, api.Name)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("write scratch file: %v", err)
		}

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the dirty-worktree block; got nil")
		}
		if !strings.Contains(err.Error(), "blocking findings") {
			t.Errorf("error = %v, want the readiness block", err)
		}

		assertWorkspaceIntact(t, h, api)
		if _, err := os.Stat(filepath.Join(wt, "scratch.txt")); err != nil {
			t.Errorf("uncommitted file was destroyed: %v", err)
		}
	})

	t.Run("filter/workspace_flag_scopes_action", func(t *testing.T) {
		// A second workspace exists on the same repo. --workspace names
		// which one dies; the sibling's worktree and branch are
		// untouched.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		const otherBranch = "story/EX-2_other"
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Other work", Type: "Story", Status: "Done",
		})
		if err := h.Run("start", "--issue", "EX-2", "--slug", "other", "--approve"); err != nil {
			t.Fatalf("start EX-2: %v", err)
		}
		otherWorktree := h.WorktreePath(otherBranch, api.Name)

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		if !api.WorktreeExists(otherWorktree) {
			t.Errorf("sibling workspace's worktree was removed")
		}
		if !api.HasBranch(otherBranch) {
			t.Errorf("sibling workspace's branch %s was deleted", otherBranch)
		}
	})

	t.Run("preview/teardown_destroys_env", func(t *testing.T) {
		// An env is bound to the issue, so the plan leads with the
		// teardown row and apply destroys it before any local
		// destruction.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		want := []string{"EX-1|brave-falcon"}
		if got := h.Preview.Destroyed(); len(got) != 1 || got[0] != want[0] {
			t.Errorf("destroyed = %v, want %v", got, want)
		}
		assertWorkspaceGone(t, h, api)
	})

	t.Run("preview/no_env_skips_teardown", func(t *testing.T) {
		// No env bound: the teardown row still appears in the plan but
		// assesses as already-done, so Destroy is never called.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		if !slices.Contains(h.Preview.Calls(), "Get") {
			t.Errorf("provider was never consulted; calls=%v", h.Preview.Calls())
		}
		if slices.Contains(h.Preview.Calls(), "Destroy") {
			t.Errorf("Destroy called with no env bound; calls=%v", h.Preview.Calls())
		}
		assertWorkspaceGone(t, h, api)
	})

	t.Run("readiness/warning_continue_proceeds", func(t *testing.T) {
		// A merged PR but an issue still In Progress: a WARN, not a
		// BLOCK. Interactively that's a Continue/Cancel dialog —
		// "y" continues and cleanup proceeds.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-1", Title: "Add feature", Type: "Story", Status: "In Progress",
		})
		h.Type("y")

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
	})

	t.Run("readiness/warning_cancel_aborts", func(t *testing.T) {
		// Same WARN, answered Cancel: ErrCancelled before the plan is
		// even built, and nothing is destroyed.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-1", Title: "Add feature", Type: "Story", Status: "In Progress",
		})
		h.Type("n")

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("error = %v, want contains \"cancelled\"", err)
		}

		assertWorkspaceIntact(t, h, api)
	})

	t.Run("force/removes_unmerged_when_flagged", func(t *testing.T) {
		// --force overrides the unmerged-work BLOCK, but doesn't make
		// the run silent: the findings still route through the
		// Continue/Cancel dialog. "y" acknowledges them and the branch
		// goes, unmerged commits and all.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, cleanupBranch, code.PullRequest{})
		commitUnmergedWork(t, h, api)
		h.Type("y")

		if err := runCleanup(h, "--force", "--approve"); err != nil {
			t.Fatalf("cleanup --force: %v", err)
		}

		assertWorkspaceGone(t, h, api)
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// A SAFE workspace with --approve and an empty stdin: neither
		// the readiness gate nor the plan gate prompts. Success with no
		// keys queued proves it.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// Dry-run renders the destroy plan but never applies:
		// ErrCancelled, and every worktree, branch, and env survives.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})

		err := runCleanup(h, "--dry-run")
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		if got := h.Preview.Destroyed(); len(got) != 0 {
			t.Errorf("dry-run destroyed %v; want none", got)
		}
		assertWorkspaceIntact(t, h, api)
		if !hasRemoteBranch(t, api, cleanupBranch) {
			t.Errorf("dry-run deleted the remote branch")
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// Without --approve the plan gate prompts; "n" selects Cancel.
		// Nothing is destroyed.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})
		h.Type("n")

		err := runCleanup(h)
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		// Pin the genuine decline: runPlanCard maps ANY confirm-form
		// error to bare ErrCancelled ("cancelled"), but only an
		// answered Cancel returns errPlanCancelled ("plan cancelled").
		// A confirm form that died on a read error — e.g. the "n" never
		// reaching it — would fail this.
		if !strings.Contains(err.Error(), "plan cancelled") {
			t.Fatalf("error = %v, want the answered-decline \"plan cancelled\"", err)
		}

		if got := h.Preview.Destroyed(); len(got) != 0 {
			t.Errorf("cancelled run destroyed %v; want none", got)
		}
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("errors/stray_files_block_workspace_removal", func(t *testing.T) {
		// Cleanup's post-apply RemoveAll would destroy anything sitting
		// in the workspace directory alongside the worktrees, so an
		// untracked file there is a BLOCK of its own.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		stray := filepath.Join(workspaceDir(h), "notes.md")
		if err := os.WriteFile(stray, []byte("scratch\n"), 0o644); err != nil {
			t.Fatalf("write stray file: %v", err)
		}

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the stray-files block; got nil")
		}
		if !strings.Contains(err.Error(), "blocking findings") {
			t.Errorf("error = %v, want the readiness block", err)
		}

		assertWorkspaceIntact(t, h, api)
		if _, err := os.Stat(stray); err != nil {
			t.Errorf("stray file was destroyed: %v", err)
		}
	})

	t.Run("errors/unmatched_workspace_repo_refuses", func(t *testing.T) {
		// The workspace holds a worktree for a repo the project's
		// repositories globs no longer match. Its main checkout can't be
		// located, and running `git branch -D` with an empty Dir would
		// aim at the caller's cwd — so cleanup refuses outright, before
		// any readiness probe or plan.
		h, repos := startCleanupWorkspace(t, "api", "web")
		api, web := repos[0], repos[1]
		markMerged(t, h, api)
		markMerged(t, h, web)
		h.Workspace.WriteConfig(strings.Replace(
			cleanupConfig, `  - "repos/*"`, `  - "repos/api"`, 1))

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the unmatched-repo refusal; got nil")
		}
		if !strings.Contains(err.Error(), "web") {
			t.Errorf("error = %v, want it to name the unmatched repo", err)
		}

		assertWorkspaceIntact(t, h, api)
		assertWorkspaceIntact(t, h, web)
	})

	t.Run("errors/preview_teardown_failure_surfaces", func(t *testing.T) {
		// The provider rejects the teardown at apply time. The error
		// propagates out of the plan apply as the command's return.
		//
		// Plan rows are independent: RunApply attempts every action and
		// returns the FIRST error, so the local destruction still runs
		// to completion. That's deliberate — a stranded preview env is
		// a remote-side loose end the user can chase from the error,
		// and holding the worktrees hostage to it would leave the
		// workspace half-cleaned on every provider hiccup. The
		// post-apply workspace-directory removal is the one step that
		// doesn't run: it sits past the runActions error return, so a
		// failed apply leaves the directory behind as the visible sign
		// the run didn't finish.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})
		h.Preview.DestroyErr = errStubErr("preview API 500: teardown refused")

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the teardown failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "teardown refused") {
			t.Errorf("error %q does not carry the provider's message", err)
		}

		if got := h.Preview.Destroyed(); len(got) != 0 {
			t.Errorf("destroyed = %v, want none (the provider refused)", got)
		}
		if api.WorktreeExists(h.WorktreePath(cleanupBranch, api.Name)) {
			t.Errorf("worktree survived; sibling rows apply independently")
		}
		if api.HasBranch(cleanupBranch) {
			t.Errorf("local branch %s survived", cleanupBranch)
		}
		if _, err := os.Stat(workspaceDir(h)); err != nil {
			t.Errorf("workspace directory removed despite the failed apply: %v", err)
		}
	})
}

// errStubErr is a minimal error for driving a fake's error knob.
type errStubErr string

func (e errStubErr) Error() string { return string(e) }
