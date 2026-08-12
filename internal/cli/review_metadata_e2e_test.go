package cli_test

// End-to-end scenarios for `bosun review`'s PER-REPOSITORY PR
// metadata: where each repo's base branch comes from, and the opt-in
// pass that lets one repo diverge from the shared answers. The broader
// review command tree (target selection, notifications, status
// transition) is covered separately; this file stays on the metadata
// seam so the two can grow independently.

import (
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// reviewConfig is the minimum project config for `bosun review`:
// repositories + workspace root, and the statuses start/review move
// through. No notification channel — the announcement is out of scope
// here and an unset channel just skips it.
const reviewConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    review: "Review"
`

const reviewBranch = "story/EX-1_feature"

// startReviewWorkspace builds a harness with the named repos, starts a
// workspace on reviewBranch, and pushes each repo's branch — a branch
// with no remote head can't be a PR's head, so review would skip it.
func startReviewWorkspace(t *testing.T, config string, names ...string) (*testharness.Harness, []*testharness.Repo) {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.WriteConfig(config)

	repos := make([]*testharness.Repo, len(names))
	for i, n := range names {
		repos[i] = h.Workspace.AddRepo(n)
	}
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story",
	})
	// --repository names the whole set explicitly so start skips its own
	// multi-repo selection form; these scenarios are about review's
	// prompts, not start's.
	args := []string{"start", "--issue", "EX-1", "--slug", "feature", "--approve"}
	for _, n := range names {
		args = append(args, "--repository", n)
	}
	if err := h.Run(args...); err != nil {
		t.Fatalf("start: %v", err)
	}
	for _, r := range repos {
		testharness.Git(t, h.WorktreePath(reviewBranch, r.Name), "push", "origin", reviewBranch)
	}
	return h, repos
}

// runReview invokes the command against the workspace with any extra
// flags appended.
func runReview(h *testharness.Harness, extra ...string) error {
	args := append([]string{
		"review", "--workspace", reviewBranch, "--issue", "EX-1",
	}, extra...)
	return h.Run(args...)
}

// baseFor returns the base branch the CreatePR request for repo used,
// failing the test when no PR was created for it.
func baseFor(t *testing.T, reqs []code.CreatePRRequest, repo string) string {
	t.Helper()
	for _, r := range reqs {
		if r.Repository == repo {
			return r.Base
		}
	}
	t.Fatalf("no CreatePR request for %q (got %+v)", repo, reqs)
	return ""
}

func TestReviewPerRepoMetadata(t *testing.T) {
	t.Run("base/per_repo_default_branch", func(t *testing.T) {
		// The headline fix: each repo's PR targets ITS OWN default
		// branch. A single workspace-wide base would have opened web's
		// PR against a branch that doesn't exist there.
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		h.Host.SeedDefaultBranch(repos[1].Owner, "web", "develop")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 2 {
			t.Fatalf("CreatePR requests = %d, want 2", len(reqs))
		}
		if got := baseFor(t, reqs, "api"); got != "main" {
			t.Errorf("api base = %q, want %q", got, "main")
		}
		if got := baseFor(t, reqs, "web"); got != "develop" {
			t.Errorf("web base = %q, want %q", got, "develop")
		}
	})

	t.Run("base/flag_overrides_every_repo", func(t *testing.T) {
		// --base is a global override — it wins over per-repo detection
		// for every repo, divergent defaults included.
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		h.Host.SeedDefaultBranch(repos[1].Owner, "web", "develop")

		if err := runReview(h, "--base", "release/2.0", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		for _, repo := range []string{"api", "web"} {
			if got := baseFor(t, h.Host.CreatePRRequests(), repo); got != "release/2.0" {
				t.Errorf("%s base = %q, want %q", repo, got, "release/2.0")
			}
		}
	})

	t.Run("base/config_overrides_every_repo", func(t *testing.T) {
		// pull_request.base is the same global override in config form.
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  base: \"trunk\"\n", "api", "web")
		h.Host.SeedDefaultBranch(repos[1].Owner, "web", "develop")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		for _, repo := range []string{"api", "web"} {
			if got := baseFor(t, h.Host.CreatePRRequests(), repo); got != "trunk" {
				t.Errorf("%s base = %q, want %q", repo, got, "trunk")
			}
		}
	})

	t.Run("base/host_failure_falls_back_to_local_clone", func(t *testing.T) {
		// A host that can't answer isn't a review failure: the local
		// clone's origin/HEAD is the next-best source. Point origin/HEAD
		// at a branch OTHER than main so the assertion distinguishes
		// "the clone answered" from "everything failed and we hit the
		// defaultBaseBranch constant" — which is also "main".
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		api := repos[0]
		testharness.Git(t, api.RemotePath, "branch", "trunk", "main")
		testharness.Git(t, api.Path, "fetch", "origin")
		testharness.Git(t, api.Path, "remote", "set-head", "origin", "trunk")
		h.Host.GetDefaultBranchErr = errReviewHost

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := baseFor(t, h.Host.CreatePRRequests(), "api"); got != "trunk" {
			t.Errorf("api base = %q, want the local clone's default branch %q", got, "trunk")
		}
	})

	t.Run("base/no_source_answers_falls_back_to_main", func(t *testing.T) {
		// Host errors AND the clone has no origin/HEAD: the constant is
		// the last resort rather than an empty base (which GitHub would
		// reject at create time).
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		testharness.Git(t, repos[0].Path, "remote", "set-head", "origin", "--delete")
		h.Host.GetDefaultBranchErr = errReviewHost

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := baseFor(t, h.Host.CreatePRRequests(), "api"); got != "main" {
			t.Errorf("api base = %q, want the %q fallback", got, "main")
		}
	})

	t.Run("body/template_renders_per_repo", func(t *testing.T) {
		// The body template embeds .BaseBranch, which now differs
		// between repos — so the body must be rendered per repo rather
		// than once from whichever repo sorted first.
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  body_template: \"Targets {{.BaseBranch}}\"\n",
			"api", "web")
		h.Host.SeedDefaultBranch(repos[1].Owner, "web", "develop")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		want := map[string]string{"api": "Targets main", "web": "Targets develop"}
		for _, r := range h.Host.CreatePRRequests() {
			if r.Body != want[r.Repository] {
				t.Errorf("%s body = %q, want %q", r.Repository, r.Body, want[r.Repository])
			}
		}
	})

	t.Run("base/retargets_an_existing_pr", func(t *testing.T) {
		// An open PR pointing at the wrong base is reconciled to the
		// repo's default branch, not left stranded.
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		h.Host.SeedDefaultBranch(repos[0].Owner, "api", "develop")
		h.Host.SeedPR(repos[0].Owner, "api", reviewBranch, code.PullRequest{
			Number: 7, State: "open", Title: "[EX-1] Add feature", BaseRef: "main",
		})

		if err := runReview(h, "--repository", "api", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		edits := h.Host.EditPRRequests()
		if len(edits) != 1 {
			t.Fatalf("EditPR requests = %d, want 1", len(edits))
		}
		if edits[0].Base != "develop" {
			t.Errorf("retargeted base = %q, want %q", edits[0].Base, "develop")
		}
	})

	t.Run("prompts/typed_answers_pin_every_repo", func(t *testing.T) {
		// The other half of the pin-vs-template split: an answer the user
		// TYPES at a shared prompt becomes the workspace-wide override and
		// lands on every repo, overriding even a divergent per-repo
		// default. Without this the whole typed path is unexercised —
		// typeaheadInputOptional could discard its input entirely and the
		// suite would stay green.
		//
		// No --reviewer/--team-reviewer/--assignee here, so all three
		// shared pickers run too.
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		owner := repos[0].Owner
		h.Host.SeedDefaultBranch(owner, "web", "develop")
		h.Host.SeedCollaborators(owner, "api", "alice")
		h.Host.SeedTeams(owner, "backend")

		h.Type("\r")             // PR-target multi-select: accept both
		h.Type("release/2.0\r")  // Base Branch: TYPED → global override
		h.Type("Custom title\r") // Title: TYPED → pinned workspace-wide
		h.Type("Custom body\r")  // Body: EDITED → pinned workspace-wide
		h.Type("\r")             // Reviewers
		h.Type("\r")             // Team Reviewers
		h.Type("\r")             // Assignees
		h.Type("\r")             // Customize: decline
		h.Type("y")              // plan gate

		if err := runReview(h, "--interactive"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 2 {
			t.Fatalf("CreatePR requests = %d, want 2; calls = %v", len(reqs), h.Host.Calls())
		}
		for _, r := range reqs {
			// web's own default is develop; the typed base must win.
			if r.Base != "release/2.0" {
				t.Errorf("%s base = %q, want the typed override %q", r.Repository, r.Base, "release/2.0")
			}
			if r.Title != "Custom title" {
				t.Errorf("%s title = %q, want the typed title", r.Repository, r.Title)
			}
			if r.Body != "Custom body" {
				t.Errorf("%s body = %q, want the edited body", r.Repository, r.Body)
			}
		}
	})

	t.Run("customize/declined_keeps_shared_metadata", func(t *testing.T) {
		// Declining is the default — the picker ships with nothing
		// checked, so one Enter is the most likely real interaction. Each
		// repo keeps its own resolved base, and no mini-form runs (with
		// every reviewer axis pinned by flag, the ONLY caller left of
		// ListCollaborators is the mini-form).
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		owner := repos[0].Owner
		h.Host.SeedDefaultBranch(owner, "web", "develop")

		h.Type("\r") // PR-target multi-select
		h.Type("\r") // Base Branch
		h.Type("\r") // Title
		h.Type("\r") // Body
		h.Type("\r") // Customize: decline
		h.Type("y")  // plan gate

		if err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := baseFor(t, h.Host.CreatePRRequests(), "api"); got != "main" {
			t.Errorf("api base = %q, want %q", got, "main")
		}
		if got := baseFor(t, h.Host.CreatePRRequests(), "web"); got != "develop" {
			t.Errorf("web base = %q, want %q", got, "develop")
		}
		if slices.Contains(h.Host.Calls(), "ListCollaborators") {
			t.Errorf("a mini-form ran after the pass was declined; calls = %v", h.Host.Calls())
		}
	})

	t.Run("customize/cancelled_aborts", func(t *testing.T) {
		// Ctrl+c at the customize picker aborts the whole run — the pass
		// sits before the plan, so nothing has been written yet and
		// nothing should be. One scenario is enough: every other form in
		// this flow propagates the same ErrCancelled the same way.
		h, _ := startReviewWorkspace(t, reviewConfig, "api", "web")

		h.Type("\r")   // PR-target multi-select
		h.Type("\r")   // Base Branch
		h.Type("\r")   // Title
		h.Type("\r")   // Body
		h.Type("\x03") // Customize: ctrl+c

		err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want contains \"cancelled\"", err)
		}
		if n := len(h.Host.CreatePRRequests()); n != 0 {
			t.Errorf("cancelled run created %d PR(s); want 0", n)
		}
	})

	t.Run("customize/option_fetch_failure_keeps_shared_values", func(t *testing.T) {
		// A repo whose collaborators and teams can't be read still gets
		// its base/title/body fields and keeps the shared reviewer sets —
		// an optional picker failing must not abort the run.
		//
		// Only the BEHAVIOUR is asserted, not the Skip rows the failure
		// emits: reporter calls are no-ops under the raw reporter every
		// harness run uses, so "the user was warned" isn't observable
		// here (see internal/testharness/README.md).
		//
		// Every reviewer axis is pinned by flag so the SHARED pickers are
		// skipped — they surface a fetch error as a hard failure, and the
		// mini-form's best-effort handling is what's under test.
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		owner := repos[0].Owner
		h.Host.SeedDefaultBranch(owner, "web", "develop")
		h.Host.ListCollaboratorsErr = errReviewHost
		h.Host.ListTeamsErr = errReviewHost

		h.Type("\r")        // PR-target multi-select
		h.Type("\r")        // Base Branch
		h.Type("\r")        // Title
		h.Type("\r")        // Body
		h.Type("\x1b[B \r") // Customize: down to web, toggle, submit
		h.Type("\r")        // web · Base Branch
		h.Type("\r")        // web · Title
		h.Type("\r")        // web · Body
		h.Type("\r")        // web · Reviewers (pre-filled from the shared set)
		h.Type("\r")        // web · Team Reviewers
		h.Type("\r")        // web · Assignees
		h.Type("y")         // plan gate

		if err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend", "--assignee", "bob"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if !slices.Contains(h.Host.Calls(), "ListCollaborators") {
			t.Fatalf("mini-form never attempted the fetch; calls = %v", h.Host.Calls())
		}
		if n := len(h.Host.CreatePRRequests()); n != 2 {
			t.Fatalf("CreatePR requests = %d, want 2", n)
		}
		if revs := h.Host.ReviewersRequested(owner, "web"); !slices.Contains(revs, "alice") {
			t.Errorf("web reviewers = %v, want the shared \"alice\" preserved", revs)
		}
		if teams := h.Host.TeamsRequested(owner, "web"); !slices.Contains(teams, "backend") {
			t.Errorf("web team reviewers = %v, want the shared \"backend\" preserved", teams)
		}
		if asns := h.Host.AssigneesAdded(owner, "web"); !slices.Contains(asns, "bob") {
			t.Errorf("web assignees = %v, want the shared \"bob\" preserved", asns)
		}
	})

	t.Run("customize/opt_in_overrides_one_repo", func(t *testing.T) {
		// The override pass: decline for api, customize web's title and
		// add a reviewer who only exists in web's collaborator list —
		// the divergence the shared prompts (fed by one repo) can't
		// express. Everything else stays on the shared answers.
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		owner := repos[0].Owner
		h.Host.SeedDefaultBranch(owner, "web", "develop")
		h.Host.SeedCollaborators(owner, "web", "dana")

		h.Type("\r")        // PR-target multi-select: accept both
		h.Type("\r")        // Base Branch: accept the per-repo rule
		h.Type("\r")        // Title: accept the templated title
		h.Type("\r")        // Body: accept
		h.Type("\r")        // Assignees: accept the self-assign default
		h.Type("\x1b[B \r") // Customize: down to web, toggle, submit
		h.Type("\r")        // web · Base Branch
		h.Type(" (web)\r")  // web · Title — append a suffix
		h.Type("\r")        // web · Body
		h.Type(" \r")       // web · Reviewers — check dana (the only option)
		h.Type("\r")        // web · Team Reviewers
		h.Type("\r")        // web · Assignees
		h.Type("y")         // plan gate

		// --team-reviewer pins the team set so its prompt is skipped;
		// reviewers stay unpinned so web's picker offers exactly dana,
		// unchecked and focused. (huh parks the cursor on the first
		// SELECTED option, so a picker with a pre-checked entry would
		// have space toggling that one off instead.)
		if err := runReview(h, "--interactive", "--team-reviewer", "backend"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if len(reqs) != 2 {
			t.Fatalf("CreatePR requests = %d, want 2; calls = %v", len(reqs), h.Host.Calls())
		}
		var apiTitle, webTitle string
		for _, r := range reqs {
			switch r.Repository {
			case "api":
				apiTitle = r.Title
			case "web":
				webTitle = r.Title
			}
		}
		if apiTitle != "[EX-1] Add feature" {
			t.Errorf("api title = %q, want the shared title", apiTitle)
		}
		if webTitle != "[EX-1] Add feature (web)" {
			t.Errorf("web title = %q, want the customized title", webTitle)
		}

		if got := h.Host.ReviewersRequested(owner, "web"); !slices.Contains(got, "dana") {
			t.Errorf("web reviewers = %v, want dana added from web's own collaborators", got)
		}
		if got := h.Host.ReviewersRequested(owner, "api"); slices.Contains(got, "dana") {
			t.Errorf("api reviewers = %v, want dana confined to web", got)
		}
	})

	t.Run("customize/single_repo_skips_the_pass", func(t *testing.T) {
		// With one repo the shared prompts WERE the per-repo prompts —
		// offering to customize would ask the same question twice, so
		// the pass never shows and the run needs no extra keystrokes.
		h, _ := startReviewWorkspace(t, reviewConfig, "api")

		h.Type("\r") // PR-target multi-select
		h.Type("\r") // Base Branch
		h.Type("\r") // Title
		h.Type("\r") // Body
		h.Type("\r") // Assignees
		h.Type("y")  // plan gate

		// Two independent signals that the pass didn't run, because a
		// declined pass leaves nothing else observable: the keystrokes
		// above are exactly enough for the prompts that SHOULD appear
		// (an extra form would starve the plan gate and hang the run),
		// and --team-reviewer pins the shared team set so ListTeams is
		// reachable only from the per-repo mini-form.
		//
		// Deliberately not asserting on h.Stdout(): harness runs use the
		// raw reporter, so it carries no card text and a "does not
		// contain" check would pass vacuously.
		if err := runReview(h, "--interactive", "--reviewer", "alice",
			"--team-reviewer", "backend"); err != nil {
			t.Fatalf("review: %v", err)
		}
		if slices.Contains(h.Host.Calls(), "ListTeams") {
			t.Errorf("customize pass ran for a single repo; calls = %v", h.Host.Calls())
		}
	})
}

// errReviewHost forces the fake host's default-branch lookup to fail.
var errReviewHost = reviewHostErr("host unavailable")

type reviewHostErr string

func (e reviewHostErr) Error() string { return string(e) }
