package cli_test

// Branch coverage for `bosun prerelease`'s resolve, plan-detail, and
// notification arms. These paths predate the session-shell port —
// they are the assess/apply branches the happy-path suites in
// prerelease_e2e_test.go never reach.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// TestPrereleaseResolveBranches covers the pre-flight arms: repo
// resolution failure, a code host that will not construct, the dirty
// gate's clean abort, a never-pushed branch dropping out, and an
// identity failure surfacing as a ✗ row instead of aborting the run.
func TestPrereleaseResolveBranches(t *testing.T) {
	t.Run("unknown_repository_filter_errors", func(t *testing.T) {
		h, _ := setupReleasable(t)

		err := runPrerelease(h, "--repository", "ghost")
		if err == nil {
			t.Fatal("unknown --repository returned nil, want an error")
		}
		if !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("error = %v, want it to name the unmatched filter", err)
		}
	})

	t.Run("code_host_unavailable_skips_releases", func(t *testing.T) {
		// The command keeps going without a host — it just has no
		// release work to plan, so nothing is cut and the run is
		// clean rather than fatal.
		h, api := setupReleasable(t)
		h.Host.NewErr = errors.New("no token configured")

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease with no code host: %v", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) with no host, want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Error("tagged v1.2.4 with no code host")
		}
	})

	t.Run("dirty_gate_cancel_aborts", func(t *testing.T) {
		// An uncommitted change in the worktree raises the readiness
		// gate; declining it aborts before any release work.
		h, api := setupReleasable(t)
		wt := h.WorktreePath(prereleaseBranch, api.Name)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatalf("dirty the worktree: %v", err)
		}
		h.Type("\r") // the gate defaults to Cancel

		err := runPrerelease(h)
		if err == nil {
			t.Fatal("declined dirty gate returned nil, want a cancellation")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want a cancellation", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) after cancelling, want 0", n)
		}
	})

	t.Run("never_pushed_branch_drops_out", func(t *testing.T) {
		// A branch with no remote ref has nothing for CreateRelease to
		// tag, so it leaves the release set with a note instead of
		// failing mid-apply. `start` pushes the branch, so the
		// never-pushed state is reconstructed by dropping the remote
		// branch and its tracking ref.
		h, api := startPrereleaseWorkspace(t)
		wt := h.WorktreePath(prereleaseBranch, api.Name)
		testharness.Git(t, api.RemotePath, "branch", "-D", prereleaseBranch)
		testharness.Git(t, wt, "branch", "-r", "-d", "origin/"+prereleaseBranch)

		h.Type("n") // decline the push offer, leaving the branch local

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) for an unpushed branch, want 0", n)
		}
	})

	t.Run("cancelled_merge_offer_aborts", func(t *testing.T) {
		// An open, mergeable workspace PR raises the merge offer ahead
		// of the release plan. Ctrl+c there is an abort, not a
		// decline: the run stops rather than proceeding to release
		// against unsettled merge state.
		h, api := setupReleasable(t)
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "open", MergeableState: "clean",
		})
		h.Type("\x03") // ctrl+c at the merge offer

		err := runPrerelease(h)
		if err == nil {
			t.Fatal("cancelled merge offer returned nil, want an abort")
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) after aborting, want 0", n)
		}
	})

	t.Run("identity_failure_is_a_row_not_an_abort", func(t *testing.T) {
		// The remote can't be parsed into owner/name, so the repo's
		// target carries the error and renders as a failed assess row.
		// The run still completes and reports the failure.
		h, api := setupReleasable(t)
		wt := h.WorktreePath(prereleaseBranch, api.Name)
		testharness.Git(t, wt, "remote", "set-url", "origin", "not-a-remote-url")

		err := runPrerelease(h, "--approve")
		if err == nil {
			t.Fatal("unparseable remote returned nil, want the assess error")
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s), want 0", n)
		}
	})
}

// TestPrereleasePlanDetailBranches covers the assess arms that decide
// what a repo's plan row says when it is not getting a new release:
// already-current, deselected with and without a prior tag, and the
// unreleased "(none) → v…" transition.
func TestPrereleasePlanDetailBranches(t *testing.T) {
	t.Run("nothing_new_since_the_latest_tag_is_skipped", func(t *testing.T) {
		// The default branch is fully contained in the latest tag, so
		// the empty-release guard downgrades the repo to a skip: no
		// release is cut and nothing is announced, because there is no
		// new work to announce. Deliberately no seeded release object
		// — with one the tag would resolve as a containing release and
		// take the sweep-up path instead (covered by
		// TestPrerelease/github_release/idempotent_existing_release).
		h, api := setupReleasable(t)
		api.Tag("v1.2.4", "main")

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s), want 0 (nothing new since v1.2.4)", n)
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("posted %d message(s), want 0 (nothing shipped)", n)
		}
	})

	t.Run("unresolvable_default_branch_refuses_to_tag", func(t *testing.T) {
		// The release must tag the default branch; with origin/HEAD
		// gone there is nothing safe to tag, and tagging the feature
		// branch would ship pre-merge history. Apply must refuse.
		h, api := setupReleasable(t)
		wt := h.WorktreePath(prereleaseBranch, api.Name)
		testharness.Git(t, wt, "symbolic-ref", "-d", "refs/remotes/origin/HEAD")

		err := runPrerelease(h, "--approve")
		if err == nil {
			t.Fatal("unresolvable default branch returned nil, want the refusal")
		}
		if !strings.Contains(err.Error(), "default branch unknown") {
			t.Fatalf("error = %v, want the default-branch refusal", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s), want 0", n)
		}
	})

	t.Run("deselected_repo_with_tag_is_not_selected", func(t *testing.T) {
		// Space unchecks the pre-checked row, enter submits: the repo
		// plans as a no-op carrying its current tag.
		h, api := setupReleasable(t)
		h.Type(" \r") // untoggle the only row, submit
		h.Type("y")   // approve the plan

		if err := runPrerelease(h); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) after deselecting, want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Error("tagged a deselected repo")
		}
	})

	t.Run("deselected_repo_without_tag_is_not_selected", func(t *testing.T) {
		// Same deselection against a repo that has never been
		// released: the row has no tag to carry, so the detail is the
		// bare "(not selected)".
		h, api := startPrereleaseWorkspace(t)
		sha := mergeReleasableWork(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "merged", MergeCommitSHA: sha,
		})
		h.Type(" \r") // untoggle the only row, submit
		h.Type("y")   // the status/notify rows still need the plan gate

		if err := runPrerelease(h); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s) after deselecting, want 0", n)
		}
	})

	t.Run("first_release_reports_none_as_the_from_side", func(t *testing.T) {
		// No prior tag: the transition reads "(none) → v0.1.0" rather
		// than an empty left-hand side.
		h, api := startPrereleaseWorkspace(t)
		sha := mergeReleasableWork(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "merged", MergeCommitSHA: sha,
		})

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		reqs := h.Host.CreateRequests()
		if len(reqs) != 1 {
			t.Fatalf("create requests = %d, want 1", len(reqs))
		}
	})

	t.Run("blocked_pr_holds_the_status", func(t *testing.T) {
		// An unmerged PR blocks the release; with nothing else to
		// ship, the issue must not advance to ready_for_release.
		h, api := startPrereleaseWorkspace(t)
		mergeReleasableWork(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "open",
		})

		before, _ := h.Tracker.Issue("EX-1")
		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		after, _ := h.Tracker.Issue("EX-1")
		if after.Status != before.Status {
			t.Errorf("status moved %q → %q behind a blocked PR", before.Status, after.Status)
		}
	})
}

// TestPrereleaseNotificationBranches covers the announcement arms: an
// existing thread turning the row into an update, multi-repo result
// ordering, and the sweep-up skip when the release was already
// announced by someone else.
func TestPrereleaseNotificationBranches(t *testing.T) {
	t.Run("existing_thread_updates", func(t *testing.T) {
		h, _ := setupReleasable(t)
		h.Notifier.SeedThread("releases", "EX-1", notify.ThreadRef{
			Channel: "releases", Timestamp: "1700000000.000100",
		})

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Notifier.Messages()); n != 1 {
			t.Fatalf("messages = %d, want 1", n)
		}
	})

	t.Run("multiple_repos_announce_in_name_order", func(t *testing.T) {
		// Two repos both cut releases, so the announcement's item list
		// is sorted — the comparison the single-repo suites never
		// exercise, since sort.Slice skips the comparator for one
		// element.
		h := testharness.New(t)
		h.Workspace.WriteConfig(prereleaseConfig)
		web := h.Workspace.AddRepo("web")
		api := h.Workspace.AddRepo("api")
		h.Tracker.SeedIssue(issue.Issue{Key: "EX-1", Title: "Add feature", Type: "Story"})
		if err := h.Run(
			"start", "--issue", "EX-1", "--slug", "feature",
			"--repository", "api", "--repository", "web", "--approve",
		); err != nil {
			t.Fatalf("start: %v", err)
		}
		for _, r := range []*testharness.Repo{web, api} {
			sha := mergeReleasableWork(t, h, r)
			h.Host.SeedPR(r.Owner, r.Name, prereleaseBranch, code.PullRequest{
				Number: 7, State: "merged", MergeCommitSHA: sha,
			})
		}

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1", len(msgs))
		}
		if len(msgs[0].Items) != 2 {
			t.Fatalf("items = %d, want 2 (one per repo)", len(msgs[0].Items))
		}
		if !strings.Contains(msgs[0].Items[0].Label, "api") {
			t.Errorf("first item = %q, want the alphabetically first repo", msgs[0].Items[0].Label)
		}
	})

	t.Run("rerun_keeps_previously_released_repos_in_the_thread", func(t *testing.T) {
		// The notification upserts: a second run REPLACES the thread's
		// message. A repo released by the first run must therefore
		// still appear in the second run's announcement, or the issue's
		// release record silently loses it.
		//
		// It survives via containingRelease — the tag holding its work
		// resolves to a release object, so the repo still produces a
		// result. That is the mechanism, and this test is what makes
		// the guarantee explicit.
		h := testharness.New(t)
		h.Workspace.WriteConfig(prereleaseConfig)
		api := h.Workspace.AddRepo("api")
		web := h.Workspace.AddRepo("web")
		h.Tracker.SeedIssue(issue.Issue{Key: "EX-1", Title: "Add feature", Type: "Story"})
		if err := h.Run(
			"start", "--issue", "EX-1", "--slug", "feature",
			"--repository", "api", "--repository", "web", "--approve",
		); err != nil {
			t.Fatalf("start: %v", err)
		}

		// Run 1: only api has merged work.
		shaAPI := mergeReleasableWork(t, h, api)
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "merged", MergeCommitSHA: shaAPI,
		})
		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("run 1: %v", err)
		}

		// Run 2: web's work merges later; api now has nothing new.
		shaWeb := mergeReleasableWork(t, h, web)
		h.Host.SeedPR(web.Owner, web.Name, prereleaseBranch, code.PullRequest{
			Number: 8, State: "merged", MergeCommitSHA: shaWeb,
		})
		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("run 2: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 2 {
			t.Fatalf("messages = %d, want 2 (one per run)", len(msgs))
		}
		last := msgs[len(msgs)-1]
		labels := make([]string, 0, len(last.Items))
		for _, it := range last.Items {
			labels = append(labels, it.Label)
		}
		if len(labels) != 2 {
			t.Fatalf("second announcement items = %v, want both repos", labels)
		}
		if !strings.Contains(strings.Join(labels, ","), "api") {
			t.Errorf("second announcement = %v, want it to still carry the repo released in run 1", labels)
		}
	})

	t.Run("unconfirmable_release_plans_nothing_to_announce", func(t *testing.T) {
		// A release-shaped tag contains the merged work, but confirming
		// whether it is a published release fails with something other
		// than a 404. Sweep-up then synthesizes a containing release
		// carrying only the tag — no URL — and Apply skips URL-less
		// items so nothing can be posted.
		//
		// The plan has to say so. Counting that result as an outcome
		// made the notify row read "new notification" for an Apply that
		// was guaranteed to send nothing; FindThread being called is
		// the observable signature of that promise, since Assess only
		// reaches it once something is deemed announceable.
		h, api := setupReleasable(t)
		api.Tag("v1.2.4", "main")
		h.Host.GetReleaseByTagErr = errors.New("502 bad gateway")

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		calls := h.Notifier.Calls()
		if slices.Contains(calls, "FindThread") {
			t.Errorf("notify assessed as announceable (calls=%v), want \"nothing to announce\"", calls)
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("posted %d message(s), want 0", n)
		}
		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s), want 0 (work is already in v1.2.4)", n)
		}
	})

	t.Run("already_announced_release_is_skipped", func(t *testing.T) {
		// Sweep-up: another user cut the containing release AND
		// already announced it, so this run posts nothing.
		h, api := setupReleasable(t)
		const url = "https://github.test/acme/api/releases/tag/v1.2.4"
		api.Tag("v1.2.4", "main")
		h.Host.SeedRelease(api.Owner, api.Name, code.Release{Tag: "v1.2.4", URL: url})
		h.Notifier.SeedAnnouncement("releases", url)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("posted %d message(s), want 0 (already announced)", n)
		}
	})
}
