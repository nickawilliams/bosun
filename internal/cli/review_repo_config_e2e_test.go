package cli_test

// End-to-end scenarios for the PER-REPOSITORY config layer as `bosun
// review` consumes it: reviewers, team reviewers, assignees, and the PR
// base resolved from each repository's own committed `.bosun.yaml`
// rather than once for the whole fan-out.
//
// These run through the real command so they cover the seam the unit
// tests can't reach — the resolution closures live inside review's
// RunE, and the thing worth pinning is what lands on each PR, not what
// an intermediate list held.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// writeWorktreeDescriptor commits a `.bosun.yaml` onto the review
// branch in the repo's WORKTREE — the checkout review actually reads.
//
// Committed and pushed rather than merely written, because that is what
// a descriptor is: a file the repository carries. Leaving it untracked
// would also trip review's dirty-tree gate and turn every scenario here
// into a test of that prompt instead.
func writeWorktreeDescriptor(t *testing.T, h *testharness.Harness, repoName, body string) {
	t.Helper()
	dir := h.WorktreePath(reviewBranch, repoName)
	if err := os.WriteFile(filepath.Join(dir, config.RepoConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	testharness.Git(t, dir, "add", config.RepoConfigFile)
	testharness.Git(t, dir, "commit", "-m", "add bosun descriptor")
	testharness.Git(t, dir, "push", "origin", reviewBranch)
}

// writeCheckoutDescriptor writes a `.bosun.yaml` into the repo's MAIN
// checkout, which review must not read.
func writeCheckoutDescriptor(t *testing.T, r *testharness.Repo, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.Path, config.RepoConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReviewPerRepoDescriptor(t *testing.T) {
	// The headline fix. Held centrally, `pull_request.reviewers` was
	// resolved ONCE for the whole multi-repo fan-out and applied to
	// every PR in it — so a reviewer who owns one repository was
	// requested on all of them, with no mechanism to vary it. Each
	// repository now answers for itself.
	t.Run("reviewers/vary_per_repo", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [alice]\n", "api", "web")
		owner := repos[0].Owner
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  reviewers: [bob]\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"alice"}) {
			t.Errorf("api reviewers = %v, want [alice] — the central list, which api never overrode", got)
		}
		if got := h.Host.ReviewersRequested(owner, "web"); !slices.Equal(got, []string{"bob"}) {
			t.Errorf("web reviewers = %v, want [bob] — its descriptor REPLACES the central list", got)
		}
	})

	// Replacement is the semantics the layer exists for, and this is
	// the half that proves it: alice must be GONE from web, not joined
	// by bob. A merge would leave the workspace-wide fan-out intact
	// under a new spelling.
	t.Run("reviewers/descriptor_replaces_rather_than_appends", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [alice, carol]\n", "web")
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  reviewers: [bob]\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		got := h.Host.ReviewersRequested(repos[0].Owner, "web")
		if slices.Contains(got, "alice") || slices.Contains(got, "carol") {
			t.Errorf("reviewers = %v, want the central names dropped entirely", got)
		}
	})

	// The opt-out. With replacement semantics an empty list is the only
	// way to say "none of the workspace's reviewers apply here", so it
	// has to survive as an explicit answer rather than reading as
	// absence and inheriting.
	t.Run("reviewers/empty_list_opts_out", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [alice]\n", "web")
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  reviewers: []\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(repos[0].Owner, "web"); len(got) != 0 {
			t.Errorf("reviewers = %v, want none requested", got)
		}
	})

	t.Run("team_reviewers/vary_per_repo", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  team_reviewers: [platform]\n", "api", "web")
		owner := repos[0].Owner
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  team_reviewers: [frontend]\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.TeamsRequested(owner, "api"); !slices.Equal(got, []string{"platform"}) {
			t.Errorf("api teams = %v, want [platform]", got)
		}
		if got := h.Host.TeamsRequested(owner, "web"); !slices.Equal(got, []string{"frontend"}) {
			t.Errorf("web teams = %v, want [frontend]", got)
		}
	})

	t.Run("assignees/vary_per_repo", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  assignees: [alice]\n"+
				"code_host:\n  pr:\n    self_assign: false\n", "api", "web")
		owner := repos[0].Owner
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  assignees: [bob]\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.AssigneesAdded(owner, "api"); !slices.Equal(got, []string{"alice"}) {
			t.Errorf("api assignees = %v, want [alice]", got)
		}
		if got := h.Host.AssigneesAdded(owner, "web"); !slices.Equal(got, []string{"bob"}) {
			t.Errorf("web assignees = %v, want [bob]", got)
		}
	})

	t.Run("base/descriptor_overrides_central", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  base: trunk\n", "api", "web")
		testharness.Git(t, repos[0].RemotePath, "branch", "release/1.0", "main")
		writeWorktreeDescriptor(t, h, "api", "pull_request:\n  base: release/1.0\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		reqs := h.Host.CreatePRRequests()
		if got := baseFor(t, reqs, "api"); got != "release/1.0" {
			t.Errorf("api base = %q, want the descriptor's value", got)
		}
		if got := baseFor(t, reqs, "web"); got != "trunk" {
			t.Errorf("web base = %q, want the central value it never overrode", got)
		}
	})

	// --base is still the workspace-wide override. A flag the user
	// typed for this run has to outrank a file committed weeks ago, or
	// the escape hatch stops being one.
	t.Run("base/flag_still_beats_the_descriptor", func(t *testing.T) {
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		testharness.Git(t, repos[0].RemotePath, "branch", "release/2.0", "main")
		writeWorktreeDescriptor(t, h, "api", "pull_request:\n  base: ignored\n")

		if err := runReview(h, "--base", "release/2.0", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := baseFor(t, h.Host.CreatePRRequests(), "api"); got != "release/2.0" {
			t.Errorf("api base = %q, want the flag to win", got)
		}
	})

	// --reviewer ADDS to whatever each repository resolved, rather than
	// replacing it. That is what the flag has always done against the
	// central list, and the per-repo layer must not quietly change it.
	t.Run("reviewers/flag_adds_to_each_repos_own", func(t *testing.T) {
		h, repos := startReviewWorkspace(t, reviewConfig, "api", "web")
		owner := repos[0].Owner
		writeWorktreeDescriptor(t, h, "web", "pull_request:\n  reviewers: [bob]\n")

		if err := runReview(h, "--reviewer", "dave", "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"dave"}) {
			t.Errorf("api reviewers = %v, want just the flag's name", got)
		}
		got := h.Host.ReviewersRequested(owner, "web")
		if !slices.Contains(got, "bob") || !slices.Contains(got, "dave") {
			t.Errorf("web reviewers = %v, want its own bob PLUS the flag's dave", got)
		}
	})

	// The branch-scoping payoff, and the reason the read is wired to
	// resolveActiveRepositories rather than resolveRepositories. A
	// branch that changes its PR policy takes effect on that branch —
	// which a central map, or a read of the main checkout, structurally
	// cannot express.
	t.Run("descriptor/read_from_the_worktree_not_the_checkout", func(t *testing.T) {
		h, repos := startReviewWorkspace(t, reviewConfig, "api")
		writeCheckoutDescriptor(t, repos[0], "pull_request:\n  reviewers: [from-checkout]\n")
		writeWorktreeDescriptor(t, h, "api", "pull_request:\n  reviewers: [from-worktree]\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		got := h.Host.ReviewersRequested(repos[0].Owner, "api")
		if !slices.Equal(got, []string{"from-worktree"}) {
			t.Errorf("reviewers = %v, want the worktree's descriptor to win", got)
		}
	})

	// A repository with no descriptor must behave exactly as it did
	// before the layer existed. This is the bootstrapping guarantee:
	// the central config is a permanent fallback, not a migration shim
	// that expires.
	t.Run("descriptor/absent_falls_back_to_central", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [alice]\n  team_reviewers: [platform]\n", "api")
		owner := repos[0].Owner

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		if got := h.Host.ReviewersRequested(owner, "api"); !slices.Equal(got, []string{"alice"}) {
			t.Errorf("reviewers = %v, want the central list", got)
		}
		if got := h.Host.TeamsRequested(owner, "api"); !slices.Equal(got, []string{"platform"}) {
			t.Errorf("teams = %v, want the central list", got)
		}
	})

	// One repository's broken file must not abort the fan-out over the
	// others: it degrades to central config and the run completes.
	t.Run("descriptor/malformed_degrades_to_central", func(t *testing.T) {
		h, repos := startReviewWorkspace(t,
			reviewConfig+"\npull_request:\n  reviewers: [alice]\n", "api", "web")
		owner := repos[0].Owner
		writeWorktreeDescriptor(t, h, "web", "pull_request: [not a map\n")

		if err := runReview(h, "--approve"); err != nil {
			t.Fatalf("review: %v", err)
		}

		for _, repo := range []string{"api", "web"} {
			if got := h.Host.ReviewersRequested(owner, repo); !slices.Equal(got, []string{"alice"}) {
				t.Errorf("%s reviewers = %v, want the central fallback", repo, got)
			}
		}
	})
}
