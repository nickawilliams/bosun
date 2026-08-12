package cli_test

// End-to-end scenarios for `bosun review`: which repositories get a PR
// opened, what each PR is created with (title, body, draft flag,
// reviewers, team reviewers, assignees), the plan gate, and the review
// notification. Per-repository metadata resolution — where each repo's
// base comes from and the opt-in customization pass — lives in
// review_metadata_e2e_test.go, which also declares the shared fixtures
// (reviewConfig, reviewBranch, startReviewWorkspace, runReview).
//
// Assertions target the call surface — which requests the code host and
// the notifier received — rather than rendered output: every harness run
// gets the raw reporter, so nothing a card would draw reaches
// h.Stdout() and an output assertion would pass vacuously. Card content
// is covered in review_card_test.go instead. See
// internal/testharness/README.md.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

// reviewNotifyConfig is reviewConfig with a review channel configured,
// so the notification action joins the plan.
const reviewNotifyConfig = reviewConfig + `
notification:
  channel_review: "code-review"
`

// reviewNoSelfAssignConfig turns off the self-assign default, so
// assignee assertions see exactly what the test asked for.
const reviewNoSelfAssignConfig = reviewConfig + `
pull_request:
  self_assign: false
`

// createdPR returns the CreatePR request for repo, failing the test when
// no PR was created for it.
func createdPR(t *testing.T, reqs []code.CreatePRRequest, repo string) code.CreatePRRequest {
	t.Helper()
	for _, r := range reqs {
		if r.Repository == repo {
			return r
		}
	}
	t.Fatalf("no CreatePR request for %q (got %+v)", repo, reqs)
	return code.CreatePRRequest{}
}

// wantCalled fails unless the code host recorded the named method.
func wantCalled(t *testing.T, h *testharness.Harness, method string) {
	t.Helper()
	if !slices.Contains(h.Host.Calls(), method) {
		t.Errorf("host.%s was never called; calls = %v", method, h.Host.Calls())
	}
}

// wantNotCalled fails when the code host recorded the named method.
func wantNotCalled(t *testing.T, h *testharness.Harness, method string) {
	t.Helper()
	if slices.Contains(h.Host.Calls(), method) {
		t.Errorf("host.%s was called; calls = %v", method, h.Host.Calls())
	}
}

// wantReported fails unless the command reported an event of kind whose
// label or value contains want. Reporter calls are recorded rather than
// rendered under the harness, so this is how "the user was told" is
// asserted.
func wantReported(t *testing.T, h *testharness.Harness, kind, want string) {
	t.Helper()
	for _, e := range h.Reporter.OfKind(kind) {
		if strings.Contains(e.Label, want) || strings.Contains(e.Value, want) {
			return
		}
	}
	t.Errorf("no %s row mentioning %q\n%s", kind, want, h.Reporter.Dump())
}

// unpublishBranch drops a repo's branch from the remote and prunes the
// local tracking ref, leaving the never-pushed state review has to skip.
// It has to be produced after the fact: start creates the branch with
// `push -u`, so every workspace repo begins life published.
func unpublishBranch(t *testing.T, h *testharness.Harness, name string) {
	t.Helper()
	wt := h.WorktreePath(reviewBranch, name)
	testharness.Git(t, wt, "push", "origin", "--delete", reviewBranch)
	testharness.Git(t, wt, "fetch", "--prune", "origin")
}

// issueStatus returns the seeded issue's current status.
func issueStatus(t *testing.T, h *testharness.Harness) string {
	t.Helper()
	iss, ok := h.Tracker.Issue("EX-1")
	if !ok {
		t.Fatalf("issue EX-1 missing from the tracker")
	}
	return iss.Status
}

func TestReview(t *testing.T) {
	t.Run("pr_creation/per_repo_pr_created", func(t *testing.T) {
		// One PR per workspace repo, each opened from that repo's own
		// worktree branch under that repo's own remote identity.
		h, _ := startReviewWorkspace(t, reviewConfig, "api", "web")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 2 {
			t.Fatalf("CreatePR requests = %d, want 2; calls = %v", len(reqs), h.Host.Calls())
		}
		for _, name := range []string{"api", "web"} {
			req := createdPR(t, reqs, name)
			if req.Owner != testharness.Owner {
				t.Errorf("%s owner = %q, want %q", name, req.Owner, testharness.Owner)
			}
			if req.Head != reviewBranch {
				t.Errorf("%s head = %q, want the workspace branch %q", name, req.Head, reviewBranch)
			}
			if req.Base != "main" {
				t.Errorf("%s base = %q, want %q", name, req.Base, "main")
			}
			if req.Draft {
				t.Errorf("%s opened as a draft without --draft", name)
			}
		}
	})

	t.Run("pr_creation/single_repo_no_prompt", func(t *testing.T) {
		// A lone repo with no PR is pre-selected, so a non-interactive
		// run needs no keystrokes and no --repository to act on it.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 || reqs[0].Repository != "api" {
			t.Fatalf("CreatePR requests = %+v, want exactly one for api", reqs)
		}
	})

	t.Run("pr_creation/multi_repo_filtered_by_flag", func(t *testing.T) {
		// --repository narrows the run: the unnamed repos never reach
		// PR resolution at all, so no PR is opened for them.
		h, _ := startReviewWorkspace(t, reviewConfig, "api", "docs", "web")

		if err := runReview(h, "--repository", "web", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 {
			t.Fatalf("CreatePR requests = %+v, want exactly one", reqs)
		}
		if reqs[0].Repository != "web" {
			t.Errorf("PR opened for %q, want the filtered repo %q", reqs[0].Repository, "web")
		}
	})

	t.Run("repository_selection/deselected_repo_is_skipped", func(t *testing.T) {
		// The PR-target form pre-checks every creatable repo; unchecking
		// one drops it from the run entirely. Both repos are offered in
		// name order, so the cursor parks on api.
		h, _ := startReviewWorkspace(t, reviewConfig, "api", "web")

		h.Type(" \r") // PR targets: uncheck api, submit
		h.Type("\r")  // Base Branch
		h.Type("\r")  // Title
		h.Type("\r")  // Body
		h.Type("y")   // plan gate

		if err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 {
			t.Fatalf("CreatePR requests = %+v, want exactly one", reqs)
		}
		if reqs[0].Repository != "web" {
			t.Errorf("PR opened for %q, want the still-checked repo %q", reqs[0].Repository, "web")
		}
	})

	t.Run("repository_selection/open_pr_can_be_checked_for_update", func(t *testing.T) {
		// Repos with an active PR are offered alongside the creatable
		// ones, unchecked. Checking one opts it into an in-place update;
		// huh parks the cursor on web (the first pre-checked option), so
		// api is one step up. (The row's "#7 (open)" label comes from
		// statusNote, covered directly in review_test.go — what the form
		// draws isn't observable from here.)
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "open", Title: "Stale", BaseRef: "main",
		})

		h.Type("\x1b[A \r") // PR targets: up to api, check it, submit
		h.Type("\r")        // Base Branch
		h.Type("\r")        // Title
		h.Type("\r")        // Body
		h.Type("\r")        // Customize: decline
		h.Type("y")         // plan gate

		if err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob"); err != nil {
			t.Fatalf("review: %v", err)
		}

		edits := h.Host.EditPRRequests()
		if len(edits) != 1 {
			t.Fatalf("EditPR requests = %+v, want one for the checked open PR", edits)
		}
		if edits[0].Number != 7 || edits[0].Title != "[EX-1] Add feature" {
			t.Errorf("edit = %+v, want PR #7 retitled from the template", edits[0])
		}
		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 || reqs[0].Repository != "web" {
			t.Errorf("CreatePR requests = %+v, want exactly one for web", reqs)
		}
	})

	t.Run("repository_selection/cancelled_aborts", func(t *testing.T) {
		// Ctrl+c at the target form is a clean abort — before any
		// metadata prompt, and long before anything is written.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		h.Type("\x03") // PR targets: ctrl+c

		err := runReview(h, "--interactive")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("cancelled run created %d PR(s); want 0", n)
		}
	})

	t.Run("idempotency/merged_pr_is_not_reopened", func(t *testing.T) {
		// Work that already landed doesn't get a second PR: a merged PR
		// leaves the repo unchecked by default, so the run is a no-op
		// against the host.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "merged", Title: "[EX-1] Add feature", BaseRef: "main",
		})

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 for an already-merged PR", n)
		}
		if n := len(h.Host.EditPRRequests()); n != 0 {
			t.Errorf("EditPR requests = %d, want 0", n)
		}
	})

	t.Run("idempotency/existing_pr_returns_no_op", func(t *testing.T) {
		// A PR that already matches the requested state is a no-op: no
		// create, no edit, no reviewer or assignee write.
		//
		// Every requested axis is seeded as already-satisfied, and every
		// one is asked for on the command line — otherwise an empty
		// request set would make the "nothing was written" assertions
		// hold no matter what planSync computed. Carol is seeded under
		// ReviewedBy rather than RequestedReviewers (GitHub drops a
		// submitter from the pending list) and in different casing, so
		// the case-insensitive fold over both lists is exercised too.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "open", Title: "[EX-1] Add feature",
			BaseRef:            "main",
			RequestedReviewers: []string{"alice"},
			ReviewedBy:         []string{"Carol"},
			RequestedTeams:     []string{"backend"},
			Assignees:          []string{"bob", "testuser"},
		})

		if err := runReview(h, "--repository", "api",
			"--reviewer", "alice", "--reviewer", "carol",
			"--team-reviewer", "backend",
			"--assignee", "bob", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 (PR #7 already exists)", n)
		}
		if n := len(h.Host.EditPRRequests()); n != 0 {
			t.Errorf("EditPR requests = %d, want 0 (PR already in sync)", n)
		}
		wantNotCalled(t, h, "RequestReviewers")
		wantNotCalled(t, h, "AddAssignees")
	})

	t.Run("idempotency/rerun_does_not_re_request", func(t *testing.T) {
		// The same thing against real prior state instead of seeded
		// state: run review twice over an untouched workspace and the
		// second run must write nothing. Re-requesting a reviewer resets
		// their submitted review on GitHub, so a duplicate here is a
		// user-visible regression, not just noise.
		args := []string{"--repository", "api", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob", "--approve"}
		h, repos := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, args...); err != nil {
			t.Fatalf("first review: %v", err)
		}
		if err := runReview(h, args...); err != nil {
			t.Fatalf("second review: %v", err)
		}

		owner := repos[0].Owner
		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"alice"}) {
			t.Errorf("reviewers = %v, want alice requested exactly once", got)
		}
		if got := h.Host.TeamsRequested(owner, "api"); !slices.Equal(got, []string{"backend"}) {
			t.Errorf("team reviewers = %v, want backend requested exactly once", got)
		}
		if got := h.Host.AssigneesAdded(owner, "api"); !slices.Equal(got, []string{"bob", "testuser"}) {
			t.Errorf("assignees = %v, want each added exactly once", got)
		}
		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Errorf("CreatePR requests = %d, want 1 across both runs", n)
		}
	})

	t.Run("idempotency/draft_pr_is_updated_in_place", func(t *testing.T) {
		// A draft PR is active, not creatable: a stale one is edited
		// rather than joined by a second PR on the same branch.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 9, State: "draft", Title: "Stale", BaseRef: "main",
		})

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 — the draft is the PR", n)
		}
		edits := h.Host.EditPRRequests()
		if len(edits) != 1 {
			t.Fatalf("EditPR requests = %+v, want one for the draft", edits)
		}
		if edits[0].Number != 9 || edits[0].Title != "[EX-1] Add feature" {
			t.Errorf("edit = %+v, want draft #9 retitled from the template", edits[0])
		}
	})

	t.Run("idempotency/open_pr_is_not_reselected", func(t *testing.T) {
		// Without --repository, a repo whose branch already has an open
		// PR isn't pre-selected — it's reported, not touched — while its
		// sibling still gets a fresh PR. Both show up in the
		// announcement, because the notification describes the whole
		// workspace rather than just this run's writes.
		h, repos := startReviewWorkspace(t, reviewNotifyConfig, "api", "web")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "open", Title: "Existing", BaseRef: "main",
			URL: "https://github.test/acme/api/pull/7",
		})

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 || reqs[0].Repository != "web" {
			t.Fatalf("CreatePR requests = %+v, want exactly one for web", reqs)
		}
		if n := len(h.Host.EditPRRequests()); n != 0 {
			t.Errorf("EditPR requests = %d, want 0 (api was not selected)", n)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1", len(msgs))
		}
		details := map[string]string{}
		for _, it := range msgs[0].Items {
			details[it.Label] = it.Detail
		}
		if details["api"] != "#7" {
			t.Errorf("api item = %q, want the pre-existing %q", details["api"], "#7")
		}
		if details["web"] == "" || details["web"] == "#7" {
			t.Errorf("web item = %q, want the freshly created PR's number", details["web"])
		}
	})

	t.Run("reviewers/from_flag", func(t *testing.T) {
		// --reviewer is repeatable and lands verbatim on the created PR,
		// as individual reviewers rather than teams.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--reviewer", "alice", "--reviewer", "bob", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		owner := repos[0].Owner
		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"alice", "bob"}) {
			t.Errorf("reviewers = %v, want [alice bob]", got)
		}
		if got := h.Host.TeamsRequested(owner, "api"); len(got) != 0 {
			t.Errorf("team reviewers = %v, want none", got)
		}
	})

	t.Run("reviewers/from_config", func(t *testing.T) {
		// Config-provided reviewers are enough on their own — no flag
		// needed. The ListCollaborators check below is a guard rather
		// than the point: a non-interactive run skips every value prompt
		// anyway, so it would hold even if config were ignored. The
		// exact-match on the requested set is what carries this.
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [\"carol\"]\n", "api")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(repos[0].Owner, "api"); !slices.Equal(got, []string{"carol"}) {
			t.Errorf("reviewers = %v, want [carol]", got)
		}
		wantNotCalled(t, h, "ListCollaborators")
	})

	t.Run("reviewers/interactive_select", func(t *testing.T) {
		// The reviewer picker offers the repo's collaborators; what the
		// user checks is what gets requested. testuser (the author) is
		// filtered out of the options, so the two remaining entries are
		// alice then bob.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		owner := repos[0].Owner
		h.Host.SeedCollaborators(owner, "api", "alice", "testuser", "bob")

		h.Type("\r")         // PR targets
		h.Type("\r")         // Base Branch
		h.Type("\r")         // Title
		h.Type("\r")         // Body
		h.Type(" \x1b[B \r") // Reviewers: check alice, down, check bob
		h.Type("y")          // plan gate

		if err := runReview(h, "--interactive",
			"--team-reviewer", "backend", "--assignee", "bob"); err != nil {
			t.Fatalf("review: %v", err)
		}

		wantCalled(t, h, "ListCollaborators")
		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"alice", "bob"}) {
			t.Errorf("reviewers = %v, want the two checked collaborators [alice bob]", got)
		}
	})

	t.Run("reviewers/author_is_dropped", func(t *testing.T) {
		// GitHub rejects a review request from the PR author with a 422.
		// The typeahead filters self out, but --reviewer bypasses it —
		// so the final list drops the author on every path.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--reviewer", "testuser", "--reviewer", "alice", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(repos[0].Owner, "api"); !slices.Equal(got, []string{"alice"}) {
			t.Errorf("reviewers = %v, want [alice] with the author dropped", got)
		}
	})

	t.Run("team_reviewers/from_flag", func(t *testing.T) {
		// --team-reviewer values travel on the team argument, not folded
		// in with individual reviewers.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--team-reviewer", "backend",
			"--team-reviewer", "platform", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		owner := repos[0].Owner
		if got := h.Host.TeamsRequested(owner, "api"); !slices.Equal(got, []string{"backend", "platform"}) {
			t.Errorf("team reviewers = %v, want [backend platform]", got)
		}
		if got := h.Host.ReviewersRequested(owner, "api"); len(got) != 0 {
			t.Errorf("individual reviewers = %v, want none", got)
		}
	})

	t.Run("assignees/from_flag", func(t *testing.T) {
		// With self-assign off, the assignee set is exactly what --assignee said.
		h, repos := startReviewWorkspace(t, reviewNoSelfAssignConfig, "api")

		if err := runReview(h, "--assignee", "bob", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.AssigneesAdded(repos[0].Owner, "api"); !slices.Equal(got, []string{"bob"}) {
			t.Errorf("assignees = %v, want [bob]", got)
		}
	})

	t.Run("assignees/self_assign_flag", func(t *testing.T) {
		// --self-assign overrides the config opt-out and adds the
		// authenticated user.
		h, repos := startReviewWorkspace(t, reviewNoSelfAssignConfig, "api")

		if err := runReview(h, "--self-assign", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.AssigneesAdded(repos[0].Owner, "api"); !slices.Equal(got, []string{"testuser"}) {
			t.Errorf("assignees = %v, want [testuser]", got)
		}
	})

	t.Run("assignees/self_assign_does_not_duplicate", func(t *testing.T) {
		// Self-assign is on by default; naming the author explicitly
		// must not assign them twice, and the casing the user typed is
		// the one that goes to the host.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--assignee", "TestUser", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.AssigneesAdded(repos[0].Owner, "api"); !slices.Equal(got, []string{"TestUser"}) {
			t.Errorf("assignees = %v, want the single [TestUser]", got)
		}
	})

	t.Run("draft/flag_creates_draft_pr", func(t *testing.T) {
		// A draft PR isn't ready for anyone: the PR is opened as a
		// draft, the issue stays where it was, and nothing is announced.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")

		if err := runReview(h, "--draft", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 {
			t.Fatalf("CreatePR requests = %+v, want exactly one", reqs)
		}
		if !reqs[0].Draft {
			t.Errorf("draft = false, want true under --draft")
		}
		if got := issueStatus(t, h); got != "In Progress" {
			t.Errorf("issue status = %q, want it left at %q", got, "In Progress")
		}
		if msgs := h.Notifier.Messages(); len(msgs) != 0 {
			t.Errorf("messages = %+v, want none for a draft", msgs)
		}
	})

	t.Run("title_body/from_template", func(t *testing.T) {
		// Both templates render against the issue and the repo's branch
		// pair rather than being passed through literally.
		h, _ := startReviewWorkspace(t, reviewConfig+
			"\npull_request:\n"+
			"  title_template: \"{{.IssueType}} {{.IssueKey}}: {{.IssueTitle}}\"\n"+
			"  body_template: \"{{.Branch}} onto {{.BaseBranch}}\"\n", "api")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		req := createdPR(t, h.Host.CreatePRRequests(), "api")
		if want := "Story EX-1: Add feature"; req.Title != want {
			t.Errorf("title = %q, want %q", req.Title, want)
		}
		if want := reviewBranch + " onto main"; req.Body != want {
			t.Errorf("body = %q, want %q", req.Body, want)
		}
	})

	t.Run("title_body/flags_override_template", func(t *testing.T) {
		// --title / --body pin literals workspace-wide, beating a
		// configured template.
		h, _ := startReviewWorkspace(t, reviewConfig+
			"\npull_request:\n"+
			"  title_template: \"[{{.IssueKey}}] templated\"\n"+
			"  body_template: \"templated body\"\n", "api")

		if err := runReview(h, "--title", "Pinned title",
			"--body", "Pinned body", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		req := createdPR(t, h.Host.CreatePRRequests(), "api")
		if req.Title != "Pinned title" {
			t.Errorf("title = %q, want the pinned literal", req.Title)
		}
		if req.Body != "Pinned body" {
			t.Errorf("body = %q, want the pinned literal", req.Body)
		}
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// --approve answers the gate up front: the plan applies with no
		// keystrokes at all, PR and issue transition included.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Errorf("CreatePR requests = %d, want 1", n)
		}
		if got := issueStatus(t, h); got != "Review" {
			t.Errorf("issue status = %q, want %q", got, "Review")
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// --dry-run renders the plan and stops: ErrCancelled out, and
		// nothing written anywhere.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")

		err := runReview(h, "--dry-run", "--approve")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 under --dry-run", n)
		}
		if got := issueStatus(t, h); got != "In Progress" {
			t.Errorf("issue status = %q, want it untouched at %q", got, "In Progress")
		}
		if msgs := h.Notifier.Messages(); len(msgs) != 0 {
			t.Errorf("messages = %+v, want none under --dry-run", msgs)
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// Declining at the gate aborts with ErrCancelled and leaves the
		// workspace exactly as it was.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		h.Type("\r") // PR targets
		h.Type("\r") // Base Branch
		h.Type("\r") // Title
		h.Type("\r") // Body
		h.Type("n")  // plan gate: decline

		err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob")
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("cancelled run created %d PR(s); want 0", n)
		}
		if got := issueStatus(t, h); got != "In Progress" {
			t.Errorf("issue status = %q, want it untouched at %q", got, "In Progress")
		}
	})

	t.Run("notification/sent_on_success", func(t *testing.T) {
		// The announcement carries one item per open PR, addressed to
		// the configured channel and keyed to the issue.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api", "web")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1; calls = %v", len(msgs), h.Notifier.Calls())
		}
		msg := msgs[0]
		if msg.Channel != "code-review" {
			t.Errorf("channel = %q, want %q", msg.Channel, "code-review")
		}
		if msg.IssueKey != "EX-1" {
			t.Errorf("issue key = %q, want %q", msg.IssueKey, "EX-1")
		}
		if msg.Title != "Add feature" {
			t.Errorf("title = %q, want the issue title", msg.Title)
		}
		labels := make([]string, len(msg.Items))
		for i, it := range msg.Items {
			labels[i] = it.Label
		}
		if !slices.Equal(labels, []string{"api", "web"}) {
			t.Errorf("item labels = %v, want [api web]", labels)
		}
		for _, it := range msg.Items {
			if !strings.Contains(it.URL, "/pull/") {
				t.Errorf("%s item URL = %q, want the created PR's URL", it.Label, it.URL)
			}
			if !strings.Contains(it.BranchURL, reviewBranch) {
				t.Errorf("%s branch URL = %q, want the reviewed branch", it.Label, it.BranchURL)
			}
		}
	})

	t.Run("notification/updated_when_a_pr_is_opened", func(t *testing.T) {
		// A PR opened by THIS run isn't in the assessed set yet, so its
		// content can't be hashed in — and here the assessed set hashes
		// identically to the thread already posted. Announcing anyway
		// (rather than trusting that hash) is what keeps web from going
		// unannounced: review api alone first, then api and web.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api", "web")

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("first review: %v", err)
		}
		if err := runReview(h, "--repository", "api",
			"--repository", "web", "--approve"); err != nil {
			t.Fatalf("second review: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 2 {
			t.Fatalf("messages = %d, want the announcement updated for web; calls = %v",
				len(msgs), h.Notifier.Calls())
		}
		if len(msgs[1].Items) != 2 {
			t.Errorf("second announcement items = %+v, want both repos", msgs[1].Items)
		}
	})

	t.Run("notification/unchanged_on_rerun", func(t *testing.T) {
		// Re-running with nothing to change leaves the announcement
		// alone: the existing thread's content hash still matches, so
		// there is no second post.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("first review: %v", err)
		}
		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("second review: %v", err)
		}

		if msgs := h.Notifier.Messages(); len(msgs) != 1 {
			t.Errorf("messages = %d after two runs, want 1", len(msgs))
		}
	})

	t.Run("notification/updated_when_content_changes", func(t *testing.T) {
		// Same PR, different body: the existing thread's hash no longer
		// matches, so the announcement is reposted with the new content.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("first review: %v", err)
		}
		if err := runReview(h, "--repository", "api",
			"--body", "Revised description", "--approve"); err != nil {
			t.Fatalf("second review: %v", err)
		}

		msgs := h.Notifier.Messages()
		if len(msgs) != 2 {
			t.Fatalf("messages = %d after a content change, want 2", len(msgs))
		}
		if len(msgs[1].Items) != 1 || msgs[1].Items[0].Body != "Revised description" {
			t.Errorf("second announcement items = %+v, want the revised body", msgs[1].Items)
		}
	})

	t.Run("notification/thread_lookup_failure_surfaces", func(t *testing.T) {
		// A lookup that fails must not read as "no thread" — that would
		// post a duplicate. It fails the row instead, and nothing is
		// posted.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")
		h.Notifier.FindThreadErr = errReviewHost

		err := runReview(h, "--approve")
		if err == nil {
			t.Fatalf("expected the thread lookup failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "finding notification thread") {
			t.Errorf("error = %v, want the thread lookup failure", err)
		}
		if msgs := h.Notifier.Messages(); len(msgs) != 0 {
			t.Errorf("messages = %+v, want none — a duplicate is worse than silence", msgs)
		}
	})

	t.Run("notification/skipped_when_channel_unconfigured", func(t *testing.T) {
		// No notification.channel_review means no announcement action at
		// all — the thread is never even looked up.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Fatalf("CreatePR requests = %d, want 1 (the run must still do its work)", n)
		}
		if calls := h.Notifier.Calls(); slices.Contains(calls, "FindThread") ||
			slices.Contains(calls, "Notify") {
			t.Errorf("notifier calls = %v, want no announcement activity", calls)
		}
		// A partial config is called out rather than silently dropped.
		wantReported(t, h, ui.CaptureSkip, "notification.channel_review")
	})

	t.Run("notification/skipped_when_notifier_unconfigured", func(t *testing.T) {
		// A channel is configured but the notifier can't be built (no
		// token, bad provider). That's not fatal: the PRs still open,
		// the run still succeeds, and nothing is announced.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")
		h.Notifier.NewErr = errors.New("notification provider not configured")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Errorf("CreatePR requests = %d, want 1", n)
		}
		if calls := h.Notifier.Calls(); len(calls) != 0 {
			t.Errorf("notifier calls = %v, want none — the fake was never handed out", calls)
		}
	})

	t.Run("errors/pr_create_failure_surfaces", func(t *testing.T) {
		// The apply-stage failure: CreatePR errors, the error reaches
		// the caller, and nothing downstream pretends the PR exists.
		h, _ := startReviewWorkspace(t, reviewNotifyConfig, "api")
		h.Host.CreatePRErr = errReviewHost

		err := runReview(h, "--approve")
		if err == nil {
			t.Fatalf("expected the CreatePR failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "host unavailable") {
			t.Errorf("error = %v, want the host failure", err)
		}
		if msgs := h.Notifier.Messages(); len(msgs) != 0 {
			t.Errorf("messages = %+v, want none — no PR was opened to announce", msgs)
		}
	})

	t.Run("errors/reviewer_and_assignee_writes_are_best_effort", func(t *testing.T) {
		// The PR exists by the time these run, so a failure is reported
		// and the run continues — losing a reviewer request is not worth
		// aborting a successful create over.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.RequestReviewersErr = errReviewHost
		h.Host.AddAssigneesErr = errReviewHost

		if err := runReview(h, "--reviewer", "alice", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Errorf("CreatePR requests = %d, want the PR to have been opened", n)
		}
		wantReported(t, h, ui.CaptureFail, "reviewers:")
		wantReported(t, h, ui.CaptureFail, "assignees:")
	})

	t.Run("errors/edit_failure_is_reported", func(t *testing.T) {
		// Same posture for an in-place content update: the edit failing
		// is a reported row, not a run-ending error.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "open", Title: "Stale", BaseRef: "main",
		})
		h.Host.EditPRErr = errReviewHost

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if n := len(h.Host.EditPRRequests()); n != 1 {
			t.Errorf("EditPR requests = %d, want the edit to have been attempted", n)
		}
		wantReported(t, h, ui.CaptureFail, "edit pr")
	})

	t.Run("errors/code_host_unavailable_skips_pr_work", func(t *testing.T) {
		// No usable code host: review says so and carries on with the
		// half of its job that doesn't need one — the issue still moves
		// to review — rather than aborting.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.NewErr = errors.New("no github token configured")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		wantReported(t, h, ui.CaptureSkip, "code host")
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 without a host", n)
		}
		if got := issueStatus(t, h); got != "Review" {
			t.Errorf("issue status = %q, want %q — the transition doesn't need a host", got, "Review")
		}
	})

	t.Run("errors/unknown_author_is_not_fatal", func(t *testing.T) {
		// The authenticated-user lookup is best-effort: it feeds
		// self-assign and the reviewer self-exclusion, and a failure
		// costs those rather than the run.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.AuthErr = errReviewHost

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		wantReported(t, h, ui.CaptureFail, "authenticated user")
		if n := len(h.Host.CreatePRRequests()); n != 1 {
			t.Errorf("CreatePR requests = %d, want 1", n)
		}
		if got := h.Host.AssigneesAdded(repos[0].Owner, "api"); len(got) != 0 {
			t.Errorf("assignees = %v, want none — there is no author to self-assign", got)
		}
	})

	t.Run("errors/unpushed_branch_is_skipped", func(t *testing.T) {
		// A branch that never reached the remote has no head ref for a
		// PR. Declining the push offer leaves web in that state: it is
		// reported and dropped, and api is reviewed as normal.
		h, _ := startReviewWorkspace(t, reviewConfig, "api", "web")
		unpublishBranch(t, h, "web")

		h.Type("n") // readiness push offer: skip

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		wantReported(t, h, ui.CaptureSkip, "not pushed")
		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 1 {
			t.Fatalf("CreatePR requests = %+v, want only the pushed repo", reqs)
		}
		if reqs[0].Repository != "api" {
			t.Errorf("PR opened for %q, want %q", reqs[0].Repository, "api")
		}
	})

	t.Run("errors/pr_lookup_failure_surfaces", func(t *testing.T) {
		// A PR-status lookup that fails becomes a failed plan row: the
		// repo is not acted on, and the run exits non-zero.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.GetPRErr = errReviewHost

		err := runReview(h, "--approve")
		if err == nil {
			t.Fatalf("expected the lookup failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "host unavailable") {
			t.Errorf("error = %v, want the host failure", err)
		}
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("CreatePR requests = %d, want 0 for a repo we couldn't assess", n)
		}
	})
}
