package cli_test

// End-to-end scenarios for `bosun status` through the test harness.
// The render layer (rows, glyphs, staleness buckets, card assembly) is
// unit-tested in status_render_test.go and status_test.go (package
// cli); this file exercises the full command path: scope resolution,
// the fan-in across tracker / code host / preview provider / workspace
// manager, and the degraded paths where a service is unconfigured.
//
// Assertion shape: status is the one command with no side effects, so
// what it *asked for* is the behavior. Every scenario asserts on the
// calls each fake recorded during the status run — which services were
// consulted, for which repos, at which refs, under which issue keys —
// plus the read-only invariant (assertStatusReadOnly).
//
// Nothing here asserts on h.Stdout(): under the harness every command
// runs against the raw reporter and card rendering draws nothing. See
// "Card output is invisible to the harness" in
// internal/testharness/README.md. Section presence and absence are
// covered by the card-builder tests in status_test.go instead.

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/testharness"
)

// statusConfig is the minimum project config for `bosun status`:
// repositories + workspace root (so the workspace manager can
// enumerate worktrees) and the status mappings the lifecycle stepper
// and project-scope sort key off.
const statusConfig = `
repositories:
  - "repos/*"
workspace:
  root: "workspaces"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    ready_for_release: "Ready for Release"
`

// statusTrackerCalls / statusHostCalls / statusPreviewCalls are the
// methods `bosun status` is permitted to reach for. Allowlists rather
// than a deny-list of today's mutators: anything new — a mutation, or
// a read-shaped method whose cost profile differs like a per-repo
// PRsInRange fan-out — fails these tests by default instead of
// slipping through until someone remembers to name it.
var (
	statusTrackerCalls = map[string]bool{"GetIssue": true, "ListIssues": true}
	statusHostCalls    = map[string]bool{"GetPRForBranch": true, "GetChecks": true}
	statusPreviewCalls = map[string]bool{"Get": true}
)

// statusProbe snapshots the fakes' call logs so a scenario can
// attribute calls to the status run alone. Workspaces are built by
// running `bosun start`, which consults the same fakes on its way
// through — take the mark after setup, read the accessors after
// status.
type statusProbe struct {
	h        *testharness.Harness
	previews *statusPreviewFactoryLog

	tracker, host, preview, refs, keys, previewKeys, factories int
}

// markStatus captures the current call counts as the baseline every
// accessor below slices from.
func markStatus(h *testharness.Harness, previews *statusPreviewFactoryLog) statusProbe {
	return statusProbe{
		h:           h,
		previews:    previews,
		tracker:     len(h.Tracker.Calls()),
		host:        len(h.Host.Calls()),
		preview:     len(h.Preview.Calls()),
		refs:        len(h.Host.ChecksRefs()),
		keys:        len(h.Tracker.GetIssueKeys()),
		previewKeys: len(h.Preview.GetKeys()),
		factories:   len(previews.names()),
	}
}

func (p statusProbe) trackerCalls() []string { return p.h.Tracker.Calls()[p.tracker:] }
func (p statusProbe) hostCalls() []string    { return p.h.Host.Calls()[p.host:] }
func (p statusProbe) previewCalls() []string { return p.h.Preview.Calls()[p.preview:] }
func (p statusProbe) checksRefs() []string   { return p.h.Host.ChecksRefs()[p.refs:] }

// issueKeys returns the keys status asked the tracker for, sorted —
// project scope fans out concurrently, so order isn't meaningful.
func (p statusProbe) issueKeys() []string {
	return sorted(p.h.Tracker.GetIssueKeys()[p.keys:])
}

// previewKeysAsked returns the issue keys status looked preview
// environments up under, sorted.
func (p statusProbe) previewKeysAsked() []string {
	return sorted(p.h.Preview.GetKeys()[p.previewKeys:])
}

// workspacesAsked returns the workspace names status passed to the
// preview-provider factory, sorted.
func (p statusProbe) workspacesAsked() []string {
	return sorted(p.previews.names()[p.factories:])
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// assertStatusReadOnly fails the test if the status run reached for
// anything outside the allowlists — the invariant that separates
// status from every other command in the tree.
func assertStatusReadOnly(t *testing.T, p statusProbe) {
	t.Helper()
	check := func(kind string, calls []string, allowed map[string]bool) {
		for _, c := range calls {
			if !allowed[c] {
				t.Errorf("status made disallowed %s call %q; calls=%v", kind, c, calls)
			}
		}
	}
	check("tracker", p.trackerCalls(), statusTrackerCalls)
	check("host", p.hostCalls(), statusHostCalls)
	check("preview", p.previewCalls(), statusPreviewCalls)
	// Absolute rather than delta-from-mark on purpose: no command in
	// any of these scenarios' setup tears an env down either, so a
	// non-empty list is a failure whenever it appeared.
	if d := p.h.Preview.Destroyed(); len(d) > 0 {
		t.Errorf("status destroyed preview env(s) %v", d)
	}
}

// statusPreviewFactoryLog records the workspace name every
// newPreviewProvider call was made with. Guarded because project scope
// builds providers from concurrent per-workspace goroutines.
type statusPreviewFactoryLog struct {
	mu sync.Mutex
	ws []string
}

func (l *statusPreviewFactoryLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ws...)
}

// installStatusPreview wires the harness's preview fake in and wraps
// the factory so the workspace name status resolves a provider for is
// observable. That name is half the workspace→env binding (the issue
// key is the other half, via Preview.GetKeys) — getting either wrong
// renders every preview as "(none)" with no other symptom, which is
// exactly the composition regression this file exists to catch.
func installStatusPreview(h *testharness.Harness) *statusPreviewFactoryLog {
	fake := h.InstallPreview()
	log := &statusPreviewFactoryLog{}
	// In-place field assignment mirrors Harness.InstallPreview itself
	// and is safe for the same reason: New() installed a fresh
	// *cli.Services for this test and restores the previous pointer in
	// t.Cleanup, so every mutation dies with the test.
	cli.GetServices().PreviewProvider = func(ws string) (preview.Provider, error) {
		log.mu.Lock()
		log.ws = append(log.ws, ws)
		log.mu.Unlock()
		return fake, nil
	}
	return log
}

// newStatusHarness builds a harness with the status config, the
// preview fake installed, and the named repos created.
func newStatusHarness(t *testing.T, repos ...string) (*testharness.Harness, *statusPreviewFactoryLog) {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.WriteConfig(statusConfig)
	for _, name := range repos {
		h.Workspace.AddRepo(name)
	}
	return h, installStatusPreview(h)
}

// startStatusWorkspace seeds an issue and runs `bosun start` to lay
// down a real workspace (branches + worktrees) for it. Returns the
// workspace name, which is also the branch name.
func startStatusWorkspace(t *testing.T, h *testharness.Harness, key, title, slug string, repos ...string) string {
	t.Helper()
	h.Tracker.SeedIssue(issue.Issue{
		Key: key, Title: title, Type: "Story", Status: "In Progress",
		URL: "https://tracker.test/browse/" + key,
	})
	args := []string{"start", "--issue", key, "--slug", slug, "--approve"}
	if len(repos) > 0 {
		args = append(args, "--repository", strings.Join(repos, ","))
	}
	if err := h.Run(args...); err != nil {
		t.Fatalf("start %s: %v", key, err)
	}
	return "story/" + key + "_" + slug
}

// statusCallCount returns how many times name appears in calls.
func statusCallCount(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
}

// equalStrings compares two string slices for exact equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStatus exercises `bosun status` end-to-end through the test
// harness. Sub-tests are grouped by the dimension they exercise
// (workspace scope, project scope, missing issue, empty workspace,
// partially-configured services) so the t.Run hierarchy doubles as the
// scenario tree for this command.
func TestStatus(t *testing.T) {
	t.Run("workspace_scope/single_repo_in_progress", func(t *testing.T) {
		// The baseline fan-in: one tracker fetch for the issue card, one
		// preview lookup for the Preview card, and a PR + checks probe
		// for the workspace's single repo.
		//
		// No --issue: the key is derived from the workspace name, which
		// is the path real invocations from inside a workspace take. The
		// derived key is what the tracker and preview lookups must both
		// receive.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-1", "Add feature", "feature")
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.trackerCalls(); len(got) != 1 || got[0] != "GetIssue" {
			t.Errorf("tracker calls = %v, want [GetIssue]", got)
		}
		if got := p.issueKeys(); !equalStrings(got, []string{"EX-1"}) {
			t.Errorf("issue keys fetched = %v, want [EX-1] (derived from the workspace name)", got)
		}
		if got := p.previewKeysAsked(); !equalStrings(got, []string{"EX-1"}) {
			t.Errorf("preview looked up under %v, want [EX-1]", got)
		}
		if got := p.workspacesAsked(); !equalStrings(got, []string{branch}) {
			t.Errorf("preview provider built for %v, want [%s]", got, branch)
		}
		if n := statusCallCount(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("GetPRForBranch calls = %d, want 1", n)
		}
		if n := statusCallCount(p.hostCalls(), "GetChecks"); n != 1 {
			t.Errorf("GetChecks calls = %d, want 1", n)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("workspace_scope/multi_repo_mixed_states", func(t *testing.T) {
		// Two repos in one workspace, in different states: api has an
		// open PR, web has none. Both must be probed — a fan-in that
		// stops at the first repo is the regression this pins — and each
		// repo's checks ref reflects its own PR state.
		h, previews := newStatusHarness(t, "api", "web")
		branch := startStatusWorkspace(t, h, "EX-2", "Wire both", "both", "api", "web")
		h.Host.SeedPR(testharness.Owner, "api", branch, code.PullRequest{
			Number: 4, State: "open", HeadSHA: "apihead", URL: "https://github.test/acme/api/pull/4",
		})
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-2"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if n := statusCallCount(p.hostCalls(), "GetPRForBranch"); n != 2 {
			t.Errorf("GetPRForBranch calls = %d, want 2 (one per repo)", n)
		}
		want := sorted([]string{"acme/api@apihead", "acme/web@" + branch})
		if got := sorted(p.checksRefs()); !equalStrings(got, want) {
			t.Errorf("checks refs = %v, want %v", got, want)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("workspace_scope/with_open_pr", func(t *testing.T) {
		// With a PR on the branch, the checks probe follows the PR's head
		// SHA rather than the branch ref — the composition in
		// fetchRepoState that a "PR shape changed" regression would
		// silently drop.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-3", "Ship it", "ship")
		h.Host.SeedPR(testharness.Owner, "api", branch, code.PullRequest{
			Number: 9, State: "open", MergeableState: "clean", Review: "approved",
			HeadSHA: "cafebabe", URL: "https://github.test/acme/api/pull/9",
		})
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-3"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.checksRefs(); !equalStrings(got, []string{"acme/api@cafebabe"}) {
			t.Errorf("checks refs = %v, want [acme/api@cafebabe]", got)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("workspace_scope/with_active_preview", func(t *testing.T) {
		// A live env bound to the issue is found and left alone: the
		// lookup runs under the issue's key (not the workspace name, not
		// the branch), Get is the only preview call, and the binding
		// survives the run.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-4", "Preview me", "preview")
		h.Preview.SeedEnv("EX-4", preview.Environment{
			Name: "brave-falcon", URL: "https://brave-falcon.preview.test",
			Probed: true, Alive: true,
		})
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-4"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.previewCalls(); !equalStrings(got, []string{"Get"}) {
			t.Errorf("preview calls = %v, want [Get]", got)
		}
		if got := p.previewKeysAsked(); !equalStrings(got, []string{"EX-4"}) {
			t.Errorf("preview looked up under %v, want [EX-4] — a wrong key reads as no env bound", got)
		}
		env, ok := h.Preview.Env("EX-4")
		if !ok || env.Name != "brave-falcon" {
			t.Errorf("preview binding after status = (%+v, %v), want brave-falcon still bound", env, ok)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("project_scope/no_workspace_flag_lists_all", func(t *testing.T) {
		// No --workspace: status resolves to project scope and fans out
		// over every workspace under the workspace root — one tracker
		// fetch and one preview provider per workspace, each keyed by
		// the issue extracted from that workspace's own name.
		h, previews := newStatusHarness(t, "api")
		first := startStatusWorkspace(t, h, "EX-5", "First", "first")
		second := startStatusWorkspace(t, h, "EX-6", "Second", "second")
		p := markStatus(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status: %v", err)
		}

		wantKeys := []string{"EX-5", "EX-6"}
		if got := p.issueKeys(); !equalStrings(got, wantKeys) {
			t.Errorf("issue keys fetched = %v, want %v (one per workspace, extracted from its name)", got, wantKeys)
		}
		if got := p.previewKeysAsked(); !equalStrings(got, wantKeys) {
			t.Errorf("preview looked up under %v, want %v", got, wantKeys)
		}
		want := sorted([]string{first, second})
		if got := p.workspacesAsked(); !equalStrings(got, want) {
			t.Errorf("preview providers built for %v, want %v", got, want)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("project_scope/per_workspace_fan_out", func(t *testing.T) {
		// Workspaces in different states are resolved independently: the
		// merged-PR workspace's checks follow its PR head, the untouched
		// one's follow its branch. Both repos are probed regardless of
		// how the first one resolved.
		//
		// (The issue called this aggregates_per_workspace_state; the
		// aggregation itself — rollup state, counts, lifecycle sort — is
		// render-only, so the name follows what's assertable here. The
		// rollup rendering is covered in status_test.go.)
		h, previews := newStatusHarness(t, "api")
		merged := startStatusWorkspace(t, h, "EX-7", "Merged work", "merged")
		open := startStatusWorkspace(t, h, "EX-8", "Fresh work", "fresh")
		h.Host.SeedPR(testharness.Owner, "api", merged, code.PullRequest{
			Number: 11, State: "merged", HeadSHA: "mergedhead",
			URL: "https://github.test/acme/api/pull/11",
		})
		p := markStatus(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if n := statusCallCount(p.hostCalls(), "GetPRForBranch"); n != 2 {
			t.Errorf("GetPRForBranch calls = %d, want 2 (one per workspace repo)", n)
		}
		want := sorted([]string{"acme/api@mergedhead", "acme/api@" + open})
		if got := sorted(p.checksRefs()); !equalStrings(got, want) {
			t.Errorf("checks refs = %v, want %v", got, want)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("issue_not_found/continues_without_issue_detail", func(t *testing.T) {
		// The tracker rejects the key (deleted issue, typo'd workspace
		// name). Status renders the degraded issue card and carries on —
		// it must not abort, and the rest of the fan-in must still run.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-9", "Vanishing", "vanish")
		h.Tracker.GetErr = errors.New("issue EX-9 not found")
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-9"); err != nil {
			t.Fatalf("status should tolerate a failed issue fetch; got %v", err)
		}

		if got := p.issueKeys(); !equalStrings(got, []string{"EX-9"}) {
			t.Errorf("issue keys fetched = %v, want [EX-9]", got)
		}
		if n := statusCallCount(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("repos not probed after issue fetch failed; GetPRForBranch = %d, want 1", n)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("clean_workspace/no_pr_no_preview", func(t *testing.T) {
		// A just-started workspace: branch pushed, no PR opened, no
		// preview env bound. Every empty state is a render concern, not
		// an error — the run succeeds and still probes each source.
		//
		// (The issue's "no branch" variant isn't reachable: a workspace
		// is a set of worktrees, each necessarily on a branch.)
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-10", "Nothing yet", "nothing")
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-10"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.previewCalls(); !equalStrings(got, []string{"Get"}) {
			t.Errorf("preview calls = %v, want [Get] (returning ErrNoEnvironment)", got)
		}
		if got := p.checksRefs(); !equalStrings(got, []string{"acme/api@" + branch}) {
			t.Errorf("checks refs = %v, want [acme/api@%s] (branch, no PR head)", got, branch)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("partial_data/host_unconfigured_skips_pr_section", func(t *testing.T) {
		// No code host (no token, no gh CLI). The PR and Checks rows have
		// no source, so status must skip them silently rather than
		// failing the run — and must not call the host at all.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-11", "Hostless", "hostless")
		cli.GetServices().CodeHost = func() (code.Host, error) {
			return nil, errors.New("code_host not configured")
		}
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-11"); err != nil {
			t.Fatalf("status should tolerate a missing code host; got %v", err)
		}

		if got := p.hostCalls(); len(got) != 0 {
			t.Errorf("host consulted despite being unconfigured: %v", got)
		}
		if got := p.issueKeys(); !equalStrings(got, []string{"EX-11"}) {
			t.Errorf("issue keys fetched = %v, want [EX-11] (issue section still renders)", got)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("partial_data/tracker_unconfigured_skips_issue_section", func(t *testing.T) {
		// No tracker. At workspace scope the whole meta block — issue
		// card, preview card, workspace card — hangs off the tracker
		// fetch, so the preview lookup is skipped along with it. The
		// per-repo cards still render.
		h, previews := newStatusHarness(t, "api")
		branch := startStatusWorkspace(t, h, "EX-12", "Trackerless", "trackerless")
		cli.GetServices().IssueTracker = func() (issue.Tracker, error) {
			return nil, errors.New("issue_tracker not configured")
		}
		p := markStatus(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-12"); err != nil {
			t.Fatalf("status should tolerate a missing tracker; got %v", err)
		}

		if got := p.trackerCalls(); len(got) != 0 {
			t.Errorf("tracker consulted despite being unconfigured: %v", got)
		}
		if got := p.previewCalls(); len(got) != 0 {
			t.Errorf("preview consulted without a tracker at workspace scope: %v", got)
		}
		if n := statusCallCount(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("repos not probed without a tracker; GetPRForBranch = %d, want 1", n)
		}
		assertStatusReadOnly(t, p)
	})

	t.Run("partial_data/project_scope_previews_without_tracker", func(t *testing.T) {
		// Project scope diverges from workspace scope here on purpose:
		// the issue key comes from the workspace *name*, so the preview
		// lookup doesn't need the tracker and still runs — under that
		// derived key. Pinning the asymmetry keeps a future "just gate
		// both on the tracker" simplification from silently dropping the
		// Preview row.
		h, previews := newStatusHarness(t, "api")
		startStatusWorkspace(t, h, "EX-13", "Trackerless project", "tp")
		cli.GetServices().IssueTracker = func() (issue.Tracker, error) {
			return nil, errors.New("issue_tracker not configured")
		}
		p := markStatus(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status should tolerate a missing tracker; got %v", err)
		}

		if got := p.trackerCalls(); len(got) != 0 {
			t.Errorf("tracker consulted despite being unconfigured: %v", got)
		}
		if got := p.previewKeysAsked(); !equalStrings(got, []string{"EX-13"}) {
			t.Errorf("preview looked up under %v, want [EX-13] (key comes from the workspace name)", got)
		}
		assertStatusReadOnly(t, p)
	})
}
