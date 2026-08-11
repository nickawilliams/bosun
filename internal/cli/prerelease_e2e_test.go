package cli_test

// End-to-end scenarios for `bosun prerelease` through the test
// harness. The pure release-target logic (gates, eligibility,
// formatting) is unit-tested in prerelease_test.go (package cli);
// this file exercises the full command path: workspace resolution,
// readiness, target selection, plan confirmation, host-side release
// creation, and the announcement notification.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// prereleaseConfig is the minimum project config for `bosun
// prerelease`: repositories + workspace root (the workspace the
// command operates on), the status mappings start/prerelease advance
// through, and the announcement channel.
const prereleaseConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    ready_for_release: "Ready for Release"
notification:
  channel_prerelease: "releases"
`

const prereleaseBranch = "story/EX-1_feature"

// startPrereleaseWorkspace builds a harness with one repo and a
// started workspace on prereleaseBranch — the baseline every
// prerelease scenario begins from.
func startPrereleaseWorkspace(t *testing.T) (*testharness.Harness, *testharness.Repo) {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.WriteConfig(prereleaseConfig)
	api := h.Workspace.AddRepo("api")
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story",
	})
	if err := h.Run("start", "--issue", "EX-1", "--slug", "feature", "--approve"); err != nil {
		t.Fatalf("start: %v", err)
	}
	return h, api
}

// mergeReleasableWork commits on the workspace branch, pushes it, and
// fast-forwards the remote's main to that commit — the end state a
// squash/FF-merged PR leaves behind. Returns the merged commit SHA.
func mergeReleasableWork(t *testing.T, h *testharness.Harness, api *testharness.Repo) string {
	t.Helper()
	wt := h.WorktreePath(prereleaseBranch, api.Name)
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	testharness.Git(t, wt, "add", "feature.txt")
	testharness.Git(t, wt, "commit", "-m", "add feature")
	testharness.Git(t, wt, "push", "origin", prereleaseBranch)
	sha := testharness.Git(t, wt, "rev-parse", "HEAD")
	testharness.Git(t, api.RemotePath, "update-ref", "refs/heads/main", sha)
	return sha
}

// setupReleasable is the release-ready baseline: a previous release
// tag v1.2.3 at the initial commit, new work merged onto main beyond
// it, and the workspace's PR recorded as merged. A prerelease run from
// here should cut the next version.
func setupReleasable(t *testing.T) (*testharness.Harness, *testharness.Repo) {
	t.Helper()
	h, api := startPrereleaseWorkspace(t)
	api.Tag("v1.2.3", "main")
	sha := mergeReleasableWork(t, h, api)
	h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
		Number: 7, State: "merged", MergeCommitSHA: sha,
	})
	return h, api
}

// runPrerelease invokes the command against the workspace with any
// extra flags appended.
func runPrerelease(h *testharness.Harness, extra ...string) error {
	args := append([]string{
		"prerelease", "--workspace", prereleaseBranch, "--issue", "EX-1",
	}, extra...)
	return h.Run(args...)
}

// TestPrerelease exercises the bosun prerelease command end-to-end
// through the test harness. Each sub-test seeds git + fake state, runs
// the command, and asserts on the created release, the remote's tags,
// the issue status, and the announcement notification.
//
// Two scenarios adapt the planned tree to the implementation as built:
// semver bumps come only from --bump (no commit-message auto-detect),
// and releases are cut published with host-generated notes (no draft
// flag on CreateReleaseRequest).
func TestPrerelease(t *testing.T) {
	t.Run("semver/patch_bump_default", func(t *testing.T) {
		// No --bump flag: the default patch bump derives v1.2.4 from
		// the previous v1.2.3.
		h, api := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		releases := h.Host.Releases()
		if len(releases) != 1 {
			t.Fatalf("releases = %d, want 1", len(releases))
		}
		if releases[0].Tag != "v1.2.4" {
			t.Errorf("tag = %q, want %q", releases[0].Tag, "v1.2.4")
		}
		if !api.HasTag("v1.2.4") {
			t.Errorf("remote missing tag v1.2.4")
		}
	})

	t.Run("semver/bump_from_flag", func(t *testing.T) {
		// --bump minor derives v1.3.0 instead of the patch default.
		h, api := setupReleasable(t)

		if err := runPrerelease(h, "--bump", "minor", "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		releases := h.Host.Releases()
		if len(releases) != 1 {
			t.Fatalf("releases = %d, want 1", len(releases))
		}
		if releases[0].Tag != "v1.3.0" {
			t.Errorf("tag = %q, want %q", releases[0].Tag, "v1.3.0")
		}
		if !api.HasTag("v1.3.0") {
			t.Errorf("remote missing tag v1.3.0")
		}
	})

	t.Run("tag/creates_at_head", func(t *testing.T) {
		// The release targets the default branch, so the tag lands on
		// main's HEAD (the merged work) — never the feature branch.
		h, api := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		reqs := h.Host.CreateRequests()
		if len(reqs) != 1 {
			t.Fatalf("create requests = %d, want 1", len(reqs))
		}
		if reqs[0].Target != "main" {
			t.Errorf("release target = %q, want %q", reqs[0].Target, "main")
		}
		tagSHA := api.RemoteRef("refs/tags/v1.2.4")
		mainSHA := api.RemoteRef("refs/heads/main")
		if tagSHA != mainSHA {
			t.Errorf("tag at %s, want main HEAD %s", tagSHA, mainSHA)
		}
	})

	t.Run("tag/idempotent_existing_tag", func(t *testing.T) {
		// The merged work is already covered by a release-shaped tag
		// (pushed without a release record). The empty-release guard
		// downgrades the run to a skip: no new tag, no release.
		h, api := setupReleasable(t)
		api.Tag("v1.2.4", "main")

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s); want 0", n)
		}
		if api.HasTag("v1.2.5") {
			t.Errorf("created tag v1.2.5; should have skipped")
		}
		// A skip (nothing new to ship) still advances the issue — the
		// distinction from a gate BLOCK, which would hold the status
		// back. The exact skip wording is locked by the
		// TestApplyEmptyReleaseGuard unit test.
		if got, _ := h.Tracker.Issue("EX-1"); got.Status != "Ready for Release" {
			t.Errorf("issue status = %q, want %q (skip still advances)", got.Status, "Ready for Release")
		}
	})

	t.Run("github_release/created_with_notes", func(t *testing.T) {
		// The release request asks the host for generated notes with
		// the changelog baseline pinned to the resolved previous tag,
		// and the returned body carries those notes.
		h, _ := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		reqs := h.Host.CreateRequests()
		if len(reqs) != 1 {
			t.Fatalf("create requests = %d, want 1", len(reqs))
		}
		req := reqs[0]
		if !req.GenerateNotes {
			t.Errorf("GenerateNotes = false, want true")
		}
		if req.PreviousTag != "v1.2.3" {
			t.Errorf("PreviousTag = %q, want %q", req.PreviousTag, "v1.2.3")
		}
		if req.Name != "v1.2.4" {
			t.Errorf("Name = %q, want %q", req.Name, "v1.2.4")
		}
		releases := h.Host.Releases()
		if len(releases) != 1 || releases[0].Body == "" {
			t.Errorf("release body empty; want generated notes")
		}
	})

	t.Run("github_release/idempotent_existing_release", func(t *testing.T) {
		// Another user already cut a release whose tag contains the
		// workspace's merged work (sweep-up). No new release is
		// created; the existing one is announced instead.
		h, api := setupReleasable(t)
		api.Tag("v1.2.4", "main")
		h.Host.SeedRelease(api.Owner, api.Name, code.Release{
			Tag:         "v1.2.4",
			URL:         "https://github.test/acme/api/releases/tag/v1.2.4",
			AuthorLogin: "alice",
		})

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s); want 0 (existing release stands in)", n)
		}
		if slices.Contains(h.Host.Calls(), "CreateRelease") {
			t.Errorf("CreateRelease called; calls=%v", h.Host.Calls())
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1 (existing release still announced)", len(msgs))
		}
		if len(msgs[0].Items) != 1 || msgs[0].Items[0].URL != "https://github.test/acme/api/releases/tag/v1.2.4" {
			t.Errorf("announcement items = %+v, want the existing release URL", msgs[0].Items)
		}
	})

	t.Run("changelog/generated_from_commits", func(t *testing.T) {
		// The host-generated release notes flow through to the
		// notification item body — the changelog the channel sees is
		// the one the host produced for the tag range.
		h, _ := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		releases := h.Host.Releases()
		if len(releases) != 1 {
			t.Fatalf("releases = %d, want 1", len(releases))
		}
		msgs := h.Notifier.Messages()
		if len(msgs) != 1 || len(msgs[0].Items) != 1 {
			t.Fatalf("messages = %+v, want 1 with 1 item", msgs)
		}
		if got := msgs[0].Items[0].Body; got != releases[0].Body {
			t.Errorf("notification body = %q, want release notes %q", got, releases[0].Body)
		}
	})

	t.Run("notification/release_announcement_sent", func(t *testing.T) {
		// The announcement lands in the configured prerelease channel
		// with the repo, version, and release URL.
		h, _ := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1", len(msgs))
		}
		msg := msgs[0]
		if msg.Channel != "releases" {
			t.Errorf("channel = %q, want %q", msg.Channel, "releases")
		}
		if msg.IssueKey != "EX-1" {
			t.Errorf("issue key = %q, want %q", msg.IssueKey, "EX-1")
		}
		if len(msg.Items) != 1 {
			t.Fatalf("items = %d, want 1", len(msg.Items))
		}
		item := msg.Items[0]
		if item.Label != "api" {
			t.Errorf("label = %q, want %q", item.Label, "api")
		}
		if item.Detail != "v1.2.4" {
			t.Errorf("detail = %q, want %q", item.Detail, "v1.2.4")
		}
		if want := h.Host.Releases()[0].URL; item.URL != want {
			t.Errorf("url = %q, want %q", item.URL, want)
		}

		got, _ := h.Tracker.Issue("EX-1")
		if got.Status != "Ready for Release" {
			t.Errorf("issue status = %q, want %q", got.Status, "Ready for Release")
		}
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// --approve with an empty stdin: neither the service-selection
		// form nor the plan gate prompts. Success with no keys proves
		// it.
		h, api := setupReleasable(t)

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		if n := len(h.Host.Releases()); n != 1 {
			t.Fatalf("releases = %d, want 1", n)
		}
		if !api.HasTag("v1.2.4") {
			t.Errorf("remote missing tag v1.2.4")
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// Dry-run still walks the interactive selection form (enter
		// keeps the pre-checked repo) but renders the plan without
		// applying: ErrCancelled, and nothing mutates anywhere.
		h, api := setupReleasable(t)
		h.Type("\r")

		err := runPrerelease(h, "--dry-run")
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("dry-run created %d release(s); want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Errorf("dry-run created tag v1.2.4")
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("dry-run sent %d notification(s); want 0", n)
		}
		if got, _ := h.Tracker.Issue("EX-1"); got.Status == "Ready for Release" {
			t.Errorf("dry-run advanced issue status; should not have")
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// Enter submits the selection form with its defaults, then "n"
		// selects Cancel at the plan gate: ErrCancelled with no
		// mutations — no release, no tag, no status change, no
		// notification.
		h, api := setupReleasable(t)
		h.Type("\r")
		h.Type("n")

		// Snapshot tracker calls: the setup's `start` run already
		// called SetStatus (→ In Progress); only calls made by the
		// cancelled prerelease run matter below.
		before := len(h.Tracker.Calls())

		err := runPrerelease(h)
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want contains \"cancelled\"", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("cancelled run created %d release(s); want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Errorf("cancelled run created tag v1.2.4")
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("cancelled run sent %d notification(s); want 0", n)
		}
		if newCalls := h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetStatus") {
			t.Errorf("cancelled run called SetStatus; calls=%v", newCalls)
		}
	})

	t.Run("errors/no_changes_since_last_release", func(t *testing.T) {
		// Everything on main is already inside the latest release tag
		// (no new work since v1.2.3). The empty-release guard skips:
		// no release, no notification, and the plan says why. The PR
		// merged without a recorded merge SHA, so sweep-up can't match
		// a containing release and the guard is what catches it.
		h, api := startPrereleaseWorkspace(t)
		api.Tag("v1.2.3", "main")
		h.Host.SeedPR(api.Owner, api.Name, prereleaseBranch, code.PullRequest{
			Number: 7, State: "merged",
		})

		if err := runPrerelease(h, "--approve"); err != nil {
			t.Fatalf("prerelease: %v", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("created %d release(s); want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Errorf("created tag v1.2.4; nothing new to release")
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("sent %d notification(s); want 0 (nothing to announce)", n)
		}
		// The skip (not a block) still advances the issue status; the
		// skip's "already released — nothing new since" reason is
		// locked by the TestApplyEmptyReleaseGuard unit test.
		if got, _ := h.Tracker.Issue("EX-1"); got.Status != "Ready for Release" {
			t.Errorf("issue status = %q, want %q (skip still advances)", got.Status, "Ready for Release")
		}
	})

	t.Run("errors/release_create_failure_surfaces", func(t *testing.T) {
		// The host rejects the release at apply time. The error
		// propagates out of the plan apply; no tag lands, no release
		// is recorded, and the announcement is never posted.
		h, api := setupReleasable(t)
		h.Host.CreateReleaseErr = errors.New("release API 500: tag refused")

		err := runPrerelease(h, "--approve")
		if err == nil {
			t.Fatalf("expected create failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "tag refused") {
			t.Errorf("error %q does not carry the host's message", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("failed create left %d release(s); want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Errorf("failed create left tag v1.2.4 on the remote")
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("failed run sent %d notification(s); want 0", n)
		}
	})
}
