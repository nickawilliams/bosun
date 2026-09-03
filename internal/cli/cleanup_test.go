package cli_test

// End-to-end scenarios for `bosun cleanup` through the test harness.
// The pure safety-matrix classification is unit-tested in
// cleanup_readiness_test.go (package cli); this file exercises the full
// command path: workspace resolution, the readiness gate, the preview
// teardown row, plan confirmation, and the real git worktree/branch
// destruction that follows.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

// cleanupConfig is the minimum project config for `bosun cleanup`:
// repositories + workspace root (so the worktrees resolve), and the
// status mappings the readiness probe reads the issue's lifecycle
// position from.
//
// workspace.root is deliberately NOT the "workspaces" default:
// Harness.WorktreePath falls back to that name when viper holds
// nothing, so a config-loading regression would still point every path
// assertion at the right directory and pass. A non-default root makes
// the assertions depend on the config actually being read.
const cleanupConfig = `
workspace:
  repositories:
    - "repos/*"
  root: "trees"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    done: "Done"
`

const cleanupBranch = "EX-1-feature"

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
	markMergedBranch(t, h, r, "EX-1", "Add feature", cleanupBranch)
}

// markMergedBranch is markMerged for an arbitrary issue/branch pair —
// the bulk scenarios run several workspaces side by side.
func markMergedBranch(t *testing.T, h *testharness.Harness, r *testharness.Repo, key, title, branch string) {
	t.Helper()
	h.Tracker.SeedIssue(issue.Issue{
		Key: key, Title: title, Type: "Story", Status: "Done",
	})
	wt := h.WorktreePath(branch, r.Name)
	sha := testharness.Git(t, wt, "rev-parse", "HEAD")
	h.Host.SeedPR(r.Owner, r.Name, branch, code.PullRequest{
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
// Cleanup takes no --repository flag: within one workspace it is
// all-or-nothing across that workspace's worktrees (--workspace picks
// which workspace dies, and every repo in it goes — covered by
// filter/). Project scope is explicit: --all sweeps every workspace,
// narrowed by the shared filter flags (--status), with sweep
// semantics — a blocked workspace is excluded and reported while its
// siblings proceed (TestCleanupBulk), where the single-workspace
// scenarios here BLOCK with an error, because an explicitly named
// target aborting and a criteria-selected set sweeping are different
// promises. And cleanup makes no tracker status transition, so the
// harness README's SetStatusErr apply-failure case has no seam here;
// errors/ covers the failure modes cleanup actually has instead.
func TestCleanup(t *testing.T) {
	t.Run("merged_branch/removes_branch_and_worktree", func(t *testing.T) {
		// The whole safety matrix reads SAFE: done-like issue, merged
		// PR whose head is what's checked out. No readiness gate, no
		// findings — cleanup destroys the worktree, both ends of the
		// branch, and the workspace directory.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		// Snapshot: the setup's `start` run already called SetStatus.
		before := len(h.Tracker.Calls())

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		// Cleanup reads the issue's status for the readiness probe but
		// never moves it — the lifecycle is the user's concern. Pinned
		// here rather than left as prose, because it's the reason this
		// file has no tracker apply-failure scenario.
		if newCalls := h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetStatus") {
			t.Errorf("cleanup called SetStatus; calls=%v", newCalls)
		}
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

		const otherBranch = "EX-2-other"
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

	t.Run("filter/no_workspace_flag_prompts_picker", func(t *testing.T) {
		// With no --workspace flag, no env, and a CWD outside any
		// workspace, RequireWorkspace falls back to the interactive
		// picker — and two workspaces make that the select list rather
		// than the single-workspace shortcut. Accepting the first entry
		// (EX-1-feature; List walks the root in sorted order) scopes
		// cleanup to that workspace and leaves the sibling untouched.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		const otherBranch = "EX-2-other"
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Other work", Type: "Story", Status: "Done",
		})
		if err := h.Run("start", "--issue", "EX-2", "--slug", "other", "--approve"); err != nil {
			t.Fatalf("start EX-2: %v", err)
		}
		otherWorktree := h.WorktreePath(otherBranch, api.Name)

		// Accept the picker's first entry.
		h.Type("\r")

		if err := h.Run("cleanup", "--issue", "EX-1", "--approve"); err != nil {
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

	t.Run("preview/picker_selection_derives_issue_for_teardown", func(t *testing.T) {
		// The path issue #100 reported: no --workspace, no --issue, CWD
		// outside any workspace. The silent issue chain runs at pre-run
		// time against a still-empty workspace and derives nothing; the
		// picker then fills the workspace, and RequireWorkspace must
		// re-derive the issue from the selected name. Without that,
		// cc.Issue stays empty — the teardown action is gated off and a
		// bound env is silently left running while the worktrees and
		// branches are destroyed. (The same empty key also degrades the
		// preamble card, but that card is unobservable here: the
		// harness's raw-mode reporter suppresses RunCardReplace output,
		// so the teardown is the consequence this test can lock.)
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})

		// A second workspace forces the select list rather than the
		// single-workspace shortcut — the reported scenario.
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Other work", Type: "Story", Status: "Done",
		})
		if err := h.Run("start", "--issue", "EX-2", "--slug", "other", "--approve"); err != nil {
			t.Fatalf("start EX-2: %v", err)
		}

		// Accept the picker's first entry (EX-1-feature; List walks
		// the root in sorted order).
		h.Type("\r")

		if err := h.Run("cleanup", "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		want := []string{"EX-1|brave-falcon"}
		if got := h.Preview.Destroyed(); len(got) != 1 || got[0] != want[0] {
			t.Errorf("destroyed = %v, want %v (env skipped: issue not derived from picked workspace)", got, want)
		}
		assertWorkspaceGone(t, h, api)
	})

	t.Run("preview/issue_flag_beats_picker_derivation", func(t *testing.T) {
		// The counterpart guard: derivation only fills a still-empty
		// issue. With --issue set, the flag's key must survive the
		// picker even when the selected workspace name carries a
		// different one — the teardown targets the flag's env, and the
		// env bound to the derivable key is left alone. An
		// unconditional overwrite after the picker would pass the test
		// above and fail here.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "calm-otter"})
		h.Preview.SeedEnv("EX-2", preview.Environment{Name: "brave-falcon"})

		// A second workspace forces the select list.
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Other work", Type: "Story", Status: "Done",
		})
		if err := h.Run("start", "--issue", "EX-2", "--slug", "other", "--approve"); err != nil {
			t.Fatalf("start EX-2: %v", err)
		}
		// start transitioned EX-2 to In Progress; put it back in a
		// done-like status so the readiness gate judges the flag's
		// issue clean.
		h.Tracker.SeedIssue(issue.Issue{
			Key: "EX-2", Title: "Other work", Type: "Story", Status: "Done",
		})

		// Accept the picker's first entry (EX-1-feature) while the
		// flag names EX-2.
		h.Type("\r")

		if err := h.Run("cleanup", "--issue", "EX-2", "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		want := []string{"EX-2|brave-falcon"}
		if got := h.Preview.Destroyed(); len(got) != 1 || got[0] != want[0] {
			t.Errorf("destroyed = %v, want %v (the flag's issue, not the picked workspace's)", got, want)
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

	t.Run("preview/unwired_provider_is_reported_not_attempted", func(t *testing.T) {
		// The provider builds but reports no backend for the destroy
		// half. Distinct from the unbuildable case below: there is a
		// provider, it simply cannot tear anything down.
		//
		// Attempting anyway is the failure this guards. cleanup
		// destroys the worktrees either way, so a Destroy that fails at
		// apply leaves the environment running behind a command that
		// already removed everything local pointing at it.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon", IssueKey: "EX-1"})
		h.Preview.ReadyErr = fmt.Errorf("%w: no CI/CD pipeline configured", preview.ErrNotConfigured)

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		if slices.Contains(h.Preview.Calls(), "Destroy") {
			t.Errorf("tore down through a provider that said it cannot; calls=%v", h.Preview.Calls())
		}
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "no CI/CD pipeline configured") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the provider's own reason never reached the user\n%s", h.Reporter.Dump())
		}
		// Still a skip, not a refusal — the local destruction proceeds.
		assertWorkspaceGone(t, h, api)
	})

	t.Run("preview/readiness_fault_does_not_block_the_local_teardown", func(t *testing.T) {
		// Answering the readiness question failed outright. That is a
		// ✗ row rather than a skip, and the exit code carries it — but
		// the sibling rows still apply, because cleanup's local
		// destruction has nothing to do with the preview backend.
		//
		// The workspace DIRECTORY survives, and deliberately so: its
		// removal is a gated plan row (RequiresPriorSuccess, scoped to
		// this workspace's group), and the ✗ readiness row closes that
		// gate. A run that could not finish leaves the directory
		// behind as the signal.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon", IssueKey: "EX-1"})
		h.Preview.ReadyErr = errors.New("resolving workflow targets: malformed target")

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatal("a readiness fault exited 0")
		}
		if !strings.Contains(err.Error(), "malformed target") {
			t.Errorf("err = %v, want the provider's diagnosis", err)
		}
		if slices.Contains(h.Preview.Calls(), "Destroy") {
			t.Errorf("tore down despite the fault; calls=%v", h.Preview.Calls())
		}
		// A fault is not a skip: it must not be filed as one.
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "malformed target") {
				t.Errorf("the fault was reported as a skip: %q", ev.Label)
			}
		}
		if api.WorktreeExists(h.WorktreePath(cleanupBranch, api.Name)) {
			t.Error("the worktree survived a fault in an unrelated row")
		}
		if api.HasBranch(cleanupBranch) {
			t.Error("the local branch survived a fault in an unrelated row")
		}
	})

	t.Run("preview/unbuildable_provider_is_reported", func(t *testing.T) {
		// A provider whose config is incomplete used not to happen: the
		// workflow-dispatch adapter declares no required keys, so
		// construction always succeeded. One that needs an API base URL
		// fails whenever that isn't set — and cleanup destroying the
		// worktrees while silently leaving the environment running is
		// the worst possible shape for that.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		prev := cli.GetServices()
		next := *prev
		next.PreviewProvider = func(string) (preview.Provider, error) {
			return nil, errors.New("preview.api.base_url not configured")
		}
		cli.SetServices(&next)
		t.Cleanup(func() { cli.SetServices(prev) })

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "preview.api.base_url") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("no skip named the unbuildable provider\n%s", h.Reporter.Dump())
		}
		// The local destruction still happens — an unreachable provider
		// is a reason to say something, not to refuse the command.
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

	t.Run("force/removes_dirty_worktree_when_flagged", func(t *testing.T) {
		// The other half of what --force means. Above, it only bypasses
		// the readiness gate: the branch's work was committed and
		// pushed, so the worktree was clean and `git worktree remove`
		// would have succeeded either way. Here the worktree is dirty,
		// which is the one case where git itself refuses without
		// --force — so this is what pins cleanup threading the flag
		// through to RemoveWorktree, and the uncommitted file really
		// being destroyed is the point rather than a side effect.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		wt := h.WorktreePath(cleanupBranch, api.Name)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("write scratch file: %v", err)
		}
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

	t.Run("cwd/escapes_the_removed_workspace", func(t *testing.T) {
		// Run from inside the workspace being destroyed: the
		// directory-removal action moves the process to the project
		// root first (the escape hatch workspace delete already had)
		// and the run says so, instead of leaving the shell stranded
		// in a deleted directory with no hint.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		t.Chdir(h.WorktreePath(cleanupBranch, api.Name))

		if err := h.Run("cleanup", "--issue", "EX-1", "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		var hinted bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureInfo) {
			if strings.Contains(ev.Label, "removed directory") {
				hinted = true
			}
		}
		if !hinted {
			t.Errorf("no cd-back hint was reported\n%s", h.Reporter.Dump())
		}
		if cwd, err := os.Getwd(); err != nil {
			t.Errorf("the process was left in a deleted directory: %v", err)
		} else if strings.Contains(cwd, cleanupBranch) {
			t.Errorf("cwd = %s, still inside the removed workspace", cwd)
		}
	})

	t.Run("cwd/nested_workspace_prunes_empty_parents", func(t *testing.T) {
		// A workspace named with a path segment ("epic/scratch")
		// leaves its parent directory behind once removed; the
		// directory action prunes junk-only parents up to the
		// workspace root — behavior that moved into the plan with the
		// directory row and must survive the move.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		if err := h.Run("workspace", "create", "epic/scratch", "api"); err != nil {
			t.Fatalf("workspace create: %v", err)
		}

		if err := h.Run("cleanup", "--workspace", "epic/scratch", "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		parent := filepath.Join(filepath.Dir(filepath.Dir(h.WorktreePath("epic/scratch", "any"))))
		if _, err := os.Stat(parent); !os.IsNotExist(err) {
			t.Errorf("empty parent %s survived (stat err = %v)", parent, err)
		}
		// The sibling workspace and the workspace root itself are
		// untouched.
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("cwd/parent_with_sibling_workspace_survives", func(t *testing.T) {
		// The counterpart guard to parent pruning: two workspaces
		// share a parent ("epic/"), one is cleaned, and the parent —
		// which still holds the sibling — must survive. The only
		// thing standing between the prune loop and the sibling is
		// the meaningful-entries check, so this is the test that
		// keeps it honest.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		if err := h.Run("workspace", "create", "epic/one", "api"); err != nil {
			t.Fatalf("workspace create epic/one: %v", err)
		}
		if err := h.Run("workspace", "create", "epic/two", "api"); err != nil {
			t.Fatalf("workspace create epic/two: %v", err)
		}

		if err := h.Run("cleanup", "--workspace", "epic/one", "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		if _, err := os.Stat(filepath.Dir(h.WorktreePath("epic/one", "any"))); !os.IsNotExist(err) {
			t.Errorf("cleaned workspace directory survived (stat err = %v)", err)
		}
		assertBranchWorkspaceIntact(t, h, api, "epic/two")
	})

	t.Run("errors/stray_files_block_workspace_removal", func(t *testing.T) {
		// Cleanup's workspace-directory removal would destroy anything
		// sitting in the workspace directory alongside the worktrees,
		// so an untracked file there is a BLOCK of its own.
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

	t.Run("errors/host_probe_failure_warns_not_blocks", func(t *testing.T) {
		// The code host is unreachable, so the PR-state probe can't
		// run. That must not read as SAFE — a failed probe means a
		// safety signal silently didn't fire — but it also isn't a
		// BLOCK: nothing is known to be lost. It lands as a WARN, which
		// interactively means the Continue/Cancel dialog. "n" declines,
		// so nothing is destroyed.
		//
		// This is the read-only analogue of the apply-failure case the
		// harness README's error-path note describes: cleanup's gather
		// phase is all probes, and this is its injectable seam.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Host.GetPRErr = errStubErr("github API 503: service unavailable")
		h.Type("n")

		err := runCleanup(h, "--approve")
		if err == nil {
			t.Fatalf("expected the unreachable host to gate the run; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("error = %v, want the declined warning gate", err)
		}

		assertWorkspaceIntact(t, h, api)
	})

	t.Run("errors/preview_probe_failure_still_tears_down", func(t *testing.T) {
		// The provider can't confirm whether an env is bound
		// (indeterminate probe, not a definitive "none"). The teardown
		// row fails open: it still applies, so a registry entry can't
		// strand because a probe blipped. The env name from the
		// partially-populated probe result is what Destroy receives.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.SeedEnv("EX-1", preview.Environment{Name: "brave-falcon"})
		h.Preview.GetErr = errStubErr("probing preview: timeout")

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		want := "EX-1|brave-falcon"
		if got := h.Preview.Destroyed(); len(got) != 1 || got[0] != want {
			t.Errorf("destroyed = %v, want [%s]", got, want)
		}
		assertWorkspaceGone(t, h, api)
	})

	t.Run("errors/preview_probe_failure_without_name_still_tears_down", func(t *testing.T) {
		// The probe fails AND carries no environment name (nothing was
		// seeded). The teardown row still fails open — Destroy runs
		// with an empty name for the provider to resolve, while the
		// plan detail shows the "(unknown)" placeholder.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Preview.GetErr = errStubErr("probing preview: timeout")

		if err := runCleanup(h, "--approve"); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		if got := h.Preview.Destroyed(); len(got) != 1 || got[0] != "EX-1|" {
			t.Errorf("destroyed = %v, want [EX-1|] (empty name passed through for the provider to resolve)", got)
		}
		assertWorkspaceGone(t, h, api)
	})

	t.Run("errors/preview_teardown_failure_surfaces", func(t *testing.T) {
		// The provider rejects the teardown at apply time. The error
		// propagates out of the plan apply as the command's return.
		//
		// These plan rows are independent, so they stay best-effort:
		// RunApply attempts every ungated action and returns the FIRST
		// error, and the local destruction runs to completion. That's
		// deliberate — a stranded preview env is
		// a remote-side loose end the user can chase from the error,
		// and holding the worktrees hostage to it would leave the
		// workspace half-cleaned on every provider hiccup. The
		// workspace-directory row is the one step that doesn't run:
		// it is the only gated action cleanup queues
		// (RequiresPriorSuccess within the workspace's group), so a
		// failed apply skips it and leaves the directory behind as
		// the visible sign the run didn't finish.
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

		// Calls(), not Destroyed(): the fake records the call before
		// consulting DestroyErr but appends to destroyed only on
		// success, so `len(Destroyed()) == 0` is true by construction
		// whenever the knob is set and would pass even if the teardown
		// row never ran at all.
		if !slices.Contains(h.Preview.Calls(), "Destroy") {
			t.Errorf("teardown row never applied; calls=%v", h.Preview.Calls())
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

// startSecondCleanupWorkspace lays down an additional workspace next
// to startCleanupWorkspace's EX-1 baseline: seeds the issue (In
// Progress, as start leaves it) and runs `bosun start`. Returns the
// workspace/branch name.
func startSecondCleanupWorkspace(t *testing.T, h *testharness.Harness, key, title, slug string) string {
	t.Helper()
	h.Tracker.SeedIssue(issue.Issue{Key: key, Title: title, Type: "Story"})
	if err := h.Run("start", "--issue", key, "--slug", slug, "--approve"); err != nil {
		t.Fatalf("start %s: %v", key, err)
	}
	return key + "-" + slug
}

// assertBranchWorkspaceGone / assertBranchWorkspaceIntact are the
// branch-parameterized versions of the EX-1 assertions above, for the
// bulk scenarios' sibling workspaces.
func assertBranchWorkspaceGone(t *testing.T, h *testharness.Harness, r *testharness.Repo, branch string) {
	t.Helper()
	if r.WorktreeExists(h.WorktreePath(branch, r.Name)) {
		t.Errorf("%s: worktree for %s still registered", r.Name, branch)
	}
	if r.HasBranch(branch) {
		t.Errorf("%s: local branch %s survived", r.Name, branch)
	}
	if _, err := os.Stat(filepath.Dir(h.WorktreePath(branch, "any"))); !os.IsNotExist(err) {
		t.Errorf("workspace directory for %s survived (stat err = %v)", branch, err)
	}
}

func assertBranchWorkspaceIntact(t *testing.T, h *testharness.Harness, r *testharness.Repo, branch string) {
	t.Helper()
	if !r.WorktreeExists(h.WorktreePath(branch, r.Name)) {
		t.Errorf("%s: worktree for %s was removed", r.Name, branch)
	}
	if !r.HasBranch(branch) {
		t.Errorf("%s: local branch %s was deleted", r.Name, branch)
	}
}

// TestCleanupBulk exercises `bosun cleanup --all` end-to-end: the
// scope grammar, the --status filter over observed issue states, the
// exclude-and-report sweep semantics for blocked workspaces, and the
// combined multi-workspace plan with per-workspace failure isolation.
func TestCleanupBulk(t *testing.T) {
	t.Run("filter/status_done_cleans_only_matching_workspaces", func(t *testing.T) {
		// Two workspaces: EX-1 done + merged, EX-2 still In Progress.
		// The filter selects EX-1 alone; EX-2 is an evaluated
		// non-match — deselected without ceremony, not an error.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		other := startSecondCleanupWorkspace(t, h, "EX-2", "Other work", "other")

		if err := h.Run("cleanup", "--all", "--status", "done", "--approve"); err != nil {
			t.Fatalf("cleanup --all --status done: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		assertBranchWorkspaceIntact(t, h, api, other)
	})

	t.Run("filter/all_without_filters_sweeps_every_workspace", func(t *testing.T) {
		// --all with no filter is the whole project: both workspaces
		// are safe (done + merged) and both go, under one plan and one
		// approval.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		other := startSecondCleanupWorkspace(t, h, "EX-2", "Other work", "other")
		markMergedBranch(t, h, api, "EX-2", "Other work", other)

		if err := h.Run("cleanup", "--all", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		assertBranchWorkspaceGone(t, h, api, other)
	})

	t.Run("readiness/blocked_workspace_excluded_while_sweep_proceeds", func(t *testing.T) {
		// Both workspaces match the filter, but EX-2's worktree is
		// dirty — a BLOCK. Sweep semantics: EX-2 is excluded and
		// reported while EX-1 proceeds, and the run still exits 0.
		// (The single-workspace command aborts on the same finding —
		// an explicitly named target and a criteria-selected set make
		// different promises.)
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		other := startSecondCleanupWorkspace(t, h, "EX-2", "Other work", "other")
		markMergedBranch(t, h, api, "EX-2", "Other work", other)
		scratch := filepath.Join(h.WorktreePath(other, api.Name), "scratch.txt")
		if err := os.WriteFile(scratch, []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("write scratch file: %v", err)
		}

		if err := h.Run("cleanup", "--all", "--status", "done", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		assertBranchWorkspaceIntact(t, h, api, other)
		if _, err := os.Stat(scratch); err != nil {
			t.Errorf("the excluded workspace's uncommitted file was destroyed: %v", err)
		}
		// The exclusion is reported, not silent: the readiness row
		// names the workspace and says it was excluded.
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureFail) {
			if strings.Contains(ev.Value, "excluded") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("no readiness row reported the exclusion\n%s", h.Reporter.Dump())
		}
	})

	t.Run("readiness/force_includes_blocked_workspaces", func(t *testing.T) {
		// Same shape with --force: the blocked workspace is included,
		// the findings route through the Continue/Cancel dialog, and
		// "y" acknowledges them — both workspaces go, dirty file and
		// all.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		other := startSecondCleanupWorkspace(t, h, "EX-2", "Other work", "other")
		markMergedBranch(t, h, api, "EX-2", "Other work", other)
		scratch := filepath.Join(h.WorktreePath(other, api.Name), "scratch.txt")
		if err := os.WriteFile(scratch, []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("write scratch file: %v", err)
		}
		h.Type("y")

		if err := h.Run("cleanup", "--all", "--status", "done", "--force", "--approve"); err != nil {
			t.Fatalf("cleanup --all --force: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		assertBranchWorkspaceGone(t, h, api, other)
	})

	t.Run("filter/unevaluable_workspace_reported_and_left_alone", func(t *testing.T) {
		// A workspace whose name carries no issue key can't be judged
		// by --status. It must not be swept, and it must not vanish
		// silently from the run either — it surfaces as a skip with
		// the reason.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		if err := h.Run("workspace", "create", "scratch", "api"); err != nil {
			t.Fatalf("workspace create: %v", err)
		}

		if err := h.Run("cleanup", "--all", "--status", "done", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceGone(t, h, api)
		assertBranchWorkspaceIntact(t, h, api, "scratch")
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "no issue key") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the unevaluable workspace was not reported\n%s", h.Reporter.Dump())
		}
	})

	t.Run("readiness/warning_prompt_cancel_aborts_sweep", func(t *testing.T) {
		// An included workspace carries a WARN (the code host is
		// unreachable, so the PR probe silently didn't run). The
		// sweep's findings collapse into one Continue/Cancel dialog;
		// "n" declines and nothing anywhere is destroyed.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Host.GetPRErr = errStubErr("github API 503: service unavailable")
		h.Type("n")

		err := h.Run("cleanup", "--all", "--status", "done", "--approve")
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("err = %v, want the declined warning gate", err)
		}
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("filter/no_matches_is_explicit_and_clean", func(t *testing.T) {
		// Nothing is done-like: the sweep says so and exits 0 —
		// nothing matched is a valid outcome, not an error, but it
		// must never be silent.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		// EX-1 stays In Progress (as start left it).

		if err := h.Run("cleanup", "--all", "--status", "done", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceIntact(t, h, api)
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "no workspaces match the filter") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("empty filter result was not reported\n%s", h.Reporter.Dump())
		}
	})

	t.Run("readiness/everything_blocked_leaves_nothing_to_sweep", func(t *testing.T) {
		// The only matching workspace is blocked (dirty): it is
		// excluded, the sweep has nothing left, and the run reports
		// that and exits 0.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		wt := h.WorktreePath(cleanupBranch, api.Name)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("write scratch file: %v", err)
		}

		if err := h.Run("cleanup", "--all", "--status", "done", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceIntact(t, h, api)
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "no workspaces passed cleanup readiness") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the empty sweep was not reported\n%s", h.Reporter.Dump())
		}
	})

	t.Run("errors/unmatched_repo_excludes_workspace_from_sweep", func(t *testing.T) {
		// The single-workspace command refuses outright when a
		// workspace repo falls outside the project's repositories
		// globs (git with an empty Dir would aim at the caller's
		// cwd). In a sweep the same hazard excludes that workspace
		// with its reason — and it is NOT force-includable, since
		// it's a correctness hazard rather than an acknowledged data
		// risk.
		h, repos := startCleanupWorkspace(t, "api", "web")
		api, web := repos[0], repos[1]
		markMerged(t, h, api)
		markMerged(t, h, web)
		h.Workspace.WriteConfig(strings.Replace(
			cleanupConfig, `  - "repos/*"`, `  - "repos/api"`, 1))

		if err := h.Run("cleanup", "--all", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		assertWorkspaceIntact(t, h, api)
		assertWorkspaceIntact(t, h, web)
		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "web") && strings.Contains(ev.Label, "not matched") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the unmatched-repo exclusion was not reported\n%s", h.Reporter.Dump())
		}
	})

	t.Run("plan_confirmation/dry_run_previews_the_sweep", func(t *testing.T) {
		// Dry-run renders the combined destroy plan and applies
		// nothing anywhere.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		other := startSecondCleanupWorkspace(t, h, "EX-2", "Other work", "other")
		markMergedBranch(t, h, api, "EX-2", "Other work", other)

		err := h.Run("cleanup", "--all", "--dry-run")
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		assertWorkspaceIntact(t, h, api)
		assertBranchWorkspaceIntact(t, h, api, other)
	})

	t.Run("filter/empty_project_reports_no_workspaces", func(t *testing.T) {
		// --all in a project with no workspaces at all: nothing to
		// observe, nothing to filter — said explicitly, exit 0.
		h := testharness.New(t)
		h.InstallPreview()
		h.Workspace.WriteConfig(cleanupConfig)
		h.Workspace.AddRepo("api")

		if err := h.Run("cleanup", "--all", "--approve"); err != nil {
			t.Fatalf("cleanup --all: %v", err)
		}

		var reported bool
		for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "no workspaces found in project") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("the empty project was not reported\n%s", h.Reporter.Dump())
		}
	})

	t.Run("errors/missing_repositories_config_fails_the_sweep", func(t *testing.T) {
		// The project's repositories globs vanished from config after
		// the workspace was created. Unlike a single unmatched repo
		// (excluded per workspace), no repo index at all means no
		// workspace could ever resolve — a config error worth failing
		// on, not sweeping past.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)
		h.Workspace.WriteConfig(strings.Replace(
			cleanupConfig, "  repositories:\n    - \"repos/*\"\n", "", 1))

		err := h.Run("cleanup", "--all", "--status", "done", "--approve")
		if err == nil || !strings.Contains(err.Error(), "no repository patterns configured") {
			t.Fatalf("err = %v, want the missing-config refusal", err)
		}
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("errors/all_conflicts_with_workspace_flag", func(t *testing.T) {
		// The scope grammar: --all and --workspace name different
		// scopes, and guessing which one wins would make the flag mean
		// different things on different invocations.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		err := h.Run("cleanup", "--all", "--workspace", cleanupBranch, "--approve")
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("err = %v, want the mutual-exclusion refusal", err)
		}
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("errors/filter_requires_all", func(t *testing.T) {
		// A filter names a population, and cleanup's default scope is
		// one workspace — the filter without --all is ambiguous, and
		// the error teaches the grammar rather than guessing.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		err := h.Run("cleanup", "--status", "done", "--approve")
		if err == nil || !strings.Contains(err.Error(), "--all") {
			t.Fatalf("err = %v, want the pass---all guidance", err)
		}
		assertWorkspaceIntact(t, h, api)
	})

	t.Run("errors/unknown_status_key_refused_up_front", func(t *testing.T) {
		// Validation happens before anything is observed: a typo'd key
		// errors immediately instead of matching nothing.
		h, repos := startCleanupWorkspace(t, "api")
		api := repos[0]
		markMerged(t, h, api)

		err := h.Run("cleanup", "--all", "--status", "Done", "--approve")
		if err == nil || !strings.Contains(err.Error(), "unknown status key") {
			t.Fatalf("err = %v, want the unknown-key rejection", err)
		}
		assertWorkspaceIntact(t, h, api)
	})
}

// errStubErr is a minimal error for driving a fake's error knob.
type errStubErr string

func (e errStubErr) Error() string { return string(e) }
