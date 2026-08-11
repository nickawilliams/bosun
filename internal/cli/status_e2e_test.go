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
// call surface each fake recorded during the status run — which
// services were consulted, for which repos, at which refs — plus the
// read-only invariant (assertReadOnly).
//
// Why not assert on rendered output: the harness runs commands with a
// non-TTY stdout, so Bootstrap installs the raw reporter and every
// card render is suppressed (ui.Card.Print short-circuits on IsRaw).
// h.Stdout() is empty for status by construction — see the deviation
// note in the PR. The rendered path is covered by the card-builder
// tests in status_test.go instead.

import (
	"errors"
	"os"
	"path/filepath"
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

// trackerReads / hostReads / previewReads are the methods `bosun
// status` is allowed to call. Allowlists rather than a deny-list of
// today's mutators: a newly added mutating method fails these tests by
// default instead of slipping through until someone remembers to name
// it.
var (
	trackerReads = map[string]bool{"GetIssue": true, "ListIssues": true}
	hostReads    = map[string]bool{
		"GetPRForBranch": true, "GetChecks": true, "GetLatestTag": true,
		"GetReleaseByTag": true, "PRsInRange": true, "ListBranches": true,
		"ListCollaborators": true, "ListTeams": true, "GetLatestDeployment": true,
		"GetAuthenticatedUser": true,
	}
	previewReads = map[string]bool{"Get": true, "Inspect": true}
)

// probe snapshots the fakes' call logs so a scenario can attribute
// calls to the status run alone. Workspaces are built by running
// `bosun start`, which mutates the tracker on its way through — mark
// after setup, read after status.
type probe struct {
	h                            *testharness.Harness
	previews                     *previewFactoryLog
	tracker, host, preview, refs int
}

// mark captures the current call counts as the baseline for since().
func mark(h *testharness.Harness, previews *previewFactoryLog) probe {
	return probe{
		h:        h,
		previews: previews,
		tracker:  len(h.Tracker.Calls()),
		host:     len(h.Host.Calls()),
		preview:  len(h.Preview.Calls()),
		refs:     len(h.Host.ChecksRefs()),
	}
}

func (p probe) trackerCalls() []string { return p.h.Tracker.Calls()[p.tracker:] }
func (p probe) hostCalls() []string    { return p.h.Host.Calls()[p.host:] }
func (p probe) previewCalls() []string { return p.h.Preview.Calls()[p.preview:] }
func (p probe) checksRefs() []string   { return p.h.Host.ChecksRefs()[p.refs:] }

// workspacesAsked returns the workspace names status passed to the
// preview-provider factory, sorted for order-independent comparison
// (project scope fans out concurrently).
func (p probe) workspacesAsked() []string {
	names := p.previews.names()
	sort.Strings(names)
	return names
}

// assertReadOnly fails the test if the status run reached for anything
// outside the read allowlists — the invariant that separates status
// from every other command in the tree.
func assertReadOnly(t *testing.T, p probe) {
	t.Helper()
	check := func(kind string, calls []string, allowed map[string]bool) {
		for _, c := range calls {
			if !allowed[c] {
				t.Errorf("status made mutating %s call %q; calls=%v", kind, c, calls)
			}
		}
	}
	check("tracker", p.trackerCalls(), trackerReads)
	check("host", p.hostCalls(), hostReads)
	check("preview", p.previewCalls(), previewReads)
	if d := p.h.Preview.Destroyed(); len(d) > 0 {
		t.Errorf("status destroyed preview env(s) %v", d)
	}
}

// previewFactoryLog records the workspace name every
// newPreviewProvider call was made with. Guarded because project scope
// builds providers from concurrent per-workspace goroutines.
type previewFactoryLog struct {
	mu sync.Mutex
	ws []string
}

func (l *previewFactoryLog) names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.ws...)
}

// installPreview wires the harness's preview fake in and wraps the
// factory so the workspace name status resolves it for is observable.
// The name is the binding between a workspace and its preview env —
// getting it wrong is exactly the composition regression this file
// exists to catch, and the fake alone can't see it.
func installPreview(h *testharness.Harness) *previewFactoryLog {
	fake := h.InstallPreview()
	log := &previewFactoryLog{}
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
func newStatusHarness(t *testing.T, repos ...string) (*testharness.Harness, *previewFactoryLog) {
	t.Helper()
	h := testharness.New(t)
	h.Workspace.WriteConfig(statusConfig)
	for _, name := range repos {
		h.Workspace.AddRepo(name)
	}
	return h, installPreview(h)
}

// startWorkspace seeds an issue and runs `bosun start` to lay down a
// real workspace (branches + worktrees) for it. Returns the workspace
// name, which is also the branch name.
func startWorkspace(t *testing.T, h *testharness.Harness, key, title, slug string, repos ...string) string {
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

// dirty writes an uncommitted file into a workspace worktree so the
// manager's IsDirty probe has something to report.
func dirty(t *testing.T, h *testharness.Harness, branch, repo string) {
	t.Helper()
	path := filepath.Join(h.WorktreePath(branch, repo), "scratch.txt")
	if err := os.WriteFile(path, []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write scratch file: %v", err)
	}
}

// countCall returns how many times name appears in calls.
func countCall(calls []string, name string) int {
	n := 0
	for _, c := range calls {
		if c == name {
			n++
		}
	}
	return n
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
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-1", "Add feature", "feature")
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-1"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.trackerCalls(); len(got) != 1 || got[0] != "GetIssue" {
			t.Errorf("tracker calls = %v, want [GetIssue]", got)
		}
		if got := p.previewCalls(); len(got) != 1 || got[0] != "Get" {
			t.Errorf("preview calls = %v, want [Get]", got)
		}
		if got := p.workspacesAsked(); len(got) != 1 || got[0] != branch {
			t.Errorf("preview provider built for %v, want [%s]", got, branch)
		}
		if n := countCall(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("GetPRForBranch calls = %d, want 1", n)
		}
		if n := countCall(p.hostCalls(), "GetChecks"); n != 1 {
			t.Errorf("GetChecks calls = %d, want 1", n)
		}
		assertReadOnly(t, p)
	})

	t.Run("workspace_scope/multi_repo_mixed_states", func(t *testing.T) {
		// Two repos in one workspace, in different states: api is dirty
		// with an open PR, web is clean with none. Both must be probed —
		// a fan-in that stops at the first repo is the regression this
		// pins — and each repo's checks ref reflects its own PR state.
		h, previews := newStatusHarness(t, "api", "web")
		branch := startWorkspace(t, h, "EX-2", "Wire both", "both", "api", "web")
		dirty(t, h, branch, "api")
		h.Host.SeedPR(testharness.Owner, "api", branch, code.PullRequest{
			Number: 4, State: "open", HeadSHA: "apihead", URL: "https://github.test/acme/api/pull/4",
		})
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-2"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if n := countCall(p.hostCalls(), "GetPRForBranch"); n != 2 {
			t.Errorf("GetPRForBranch calls = %d, want 2 (one per repo)", n)
		}
		want := []string{"acme/api@apihead", "acme/web@" + branch}
		got := p.checksRefs()
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("checks refs = %v, want %v", got, want)
		}
		assertReadOnly(t, p)
	})

	t.Run("workspace_scope/with_open_pr", func(t *testing.T) {
		// With a PR on the branch, the checks probe follows the PR's head
		// SHA rather than the branch ref — the composition in
		// fetchRepoState that a "PR shape changed" regression would
		// silently drop.
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-3", "Ship it", "ship")
		h.Host.SeedPR(testharness.Owner, "api", branch, code.PullRequest{
			Number: 9, State: "open", MergeableState: "clean", Review: "approved",
			HeadSHA: "cafebabe", URL: "https://github.test/acme/api/pull/9",
		})
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-3"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.checksRefs(); len(got) != 1 || got[0] != "acme/api@cafebabe" {
			t.Errorf("checks refs = %v, want [acme/api@cafebabe]", got)
		}
		assertReadOnly(t, p)
	})

	t.Run("workspace_scope/with_active_preview", func(t *testing.T) {
		// A live env bound to the issue is read, never touched: Get is
		// the only preview call and the binding survives the run.
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-4", "Preview me", "preview")
		h.Preview.SeedEnv("EX-4", preview.Environment{
			Name: "brave-falcon", URL: "https://brave-falcon.preview.test",
			Probed: true, Alive: true,
		})
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-4"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.previewCalls(); len(got) != 1 || got[0] != "Get" {
			t.Errorf("preview calls = %v, want [Get]", got)
		}
		env, ok := h.Preview.Env("EX-4")
		if !ok || env.Name != "brave-falcon" {
			t.Errorf("preview binding after status = (%+v, %v), want brave-falcon still bound", env, ok)
		}
		assertReadOnly(t, p)
	})

	t.Run("project_scope/no_workspace_flag_lists_all", func(t *testing.T) {
		// No --workspace: status resolves to project scope and fans out
		// over every workspace under the workspace root — one tracker
		// fetch and one preview provider per workspace, keyed by that
		// workspace's own name.
		h, previews := newStatusHarness(t, "api")
		first := startWorkspace(t, h, "EX-5", "First", "first")
		second := startWorkspace(t, h, "EX-6", "Second", "second")
		p := mark(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if n := countCall(p.trackerCalls(), "GetIssue"); n != 2 {
			t.Errorf("GetIssue calls = %d, want 2 (one per workspace)", n)
		}
		want := []string{first, second}
		sort.Strings(want)
		if got := p.workspacesAsked(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("preview providers built for %v, want %v", got, want)
		}
		if n := countCall(p.previewCalls(), "Get"); n != 2 {
			t.Errorf("preview Get calls = %d, want 2", n)
		}
		assertReadOnly(t, p)
	})

	t.Run("project_scope/aggregates_per_workspace_state", func(t *testing.T) {
		// Workspaces in different states roll up independently: the
		// merged-PR workspace's checks follow its PR head, the untouched
		// one's follow its branch. Both repos are probed regardless of
		// how the first one resolved.
		h, previews := newStatusHarness(t, "api")
		merged := startWorkspace(t, h, "EX-7", "Merged work", "merged")
		open := startWorkspace(t, h, "EX-8", "Fresh work", "fresh")
		h.Host.SeedPR(testharness.Owner, "api", merged, code.PullRequest{
			Number: 11, State: "merged", HeadSHA: "mergedhead",
			URL: "https://github.test/acme/api/pull/11",
		})
		p := mark(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if n := countCall(p.hostCalls(), "GetPRForBranch"); n != 2 {
			t.Errorf("GetPRForBranch calls = %d, want 2 (one per workspace repo)", n)
		}
		want := []string{"acme/api@mergedhead", "acme/api@" + open}
		got := p.checksRefs()
		sort.Strings(got)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("checks refs = %v, want %v", got, want)
		}
		assertReadOnly(t, p)
	})

	t.Run("issue_not_found/renders_without_issue_detail", func(t *testing.T) {
		// The tracker rejects the key (deleted issue, typo'd workspace
		// name). Status renders the degraded issue card and carries on —
		// it must not abort, and the rest of the fan-in must still run.
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-9", "Vanishing", "vanish")
		h.Tracker.GetErr = errors.New("issue EX-9 not found")
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-9"); err != nil {
			t.Fatalf("status should tolerate a failed issue fetch; got %v", err)
		}

		if n := countCall(p.trackerCalls(), "GetIssue"); n != 1 {
			t.Errorf("GetIssue calls = %d, want 1", n)
		}
		if n := countCall(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("repos not probed after issue fetch failed; GetPRForBranch = %d, want 1", n)
		}
		assertReadOnly(t, p)
	})

	t.Run("clean_workspace/no_pr_no_preview", func(t *testing.T) {
		// A just-started workspace: branch pushed, no PR opened, no
		// preview env bound. Every empty state is a render concern, not
		// an error — the run succeeds and still probes each source.
		//
		// (The issue's "no branch" variant isn't reachable: a workspace
		// is a set of worktrees, each necessarily on a branch.)
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-10", "Nothing yet", "nothing")
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-10"); err != nil {
			t.Fatalf("status: %v", err)
		}

		if got := p.previewCalls(); len(got) != 1 || got[0] != "Get" {
			t.Errorf("preview calls = %v, want [Get] (returning ErrNoEnvironment)", got)
		}
		if got := p.checksRefs(); len(got) != 1 || got[0] != "acme/api@"+branch {
			t.Errorf("checks refs = %v, want [acme/api@%s] (branch, no PR head)", got, branch)
		}
		assertReadOnly(t, p)
	})

	t.Run("partial_data/host_unconfigured_skips_pr_section", func(t *testing.T) {
		// No code host (no token, no gh CLI). The PR and Checks rows have
		// no source, so status must skip them silently rather than
		// failing the run — and must not call the host at all.
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-11", "Hostless", "hostless")
		cli.GetServices().CodeHost = func() (code.Host, error) {
			return nil, errors.New("code_host not configured")
		}
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-11"); err != nil {
			t.Fatalf("status should tolerate a missing code host; got %v", err)
		}

		if got := p.hostCalls(); len(got) != 0 {
			t.Errorf("host consulted despite being unconfigured: %v", got)
		}
		if n := countCall(p.trackerCalls(), "GetIssue"); n != 1 {
			t.Errorf("GetIssue calls = %d, want 1 (issue section still renders)", n)
		}
		assertReadOnly(t, p)
	})

	t.Run("partial_data/tracker_unconfigured_skips_issue_section", func(t *testing.T) {
		// No tracker. At workspace scope the whole meta block — issue
		// card, preview card, workspace card — hangs off the tracker
		// fetch, so the preview lookup is skipped along with it. The
		// per-repo cards still render.
		h, previews := newStatusHarness(t, "api")
		branch := startWorkspace(t, h, "EX-12", "Trackerless", "trackerless")
		cli.GetServices().IssueTracker = func() (issue.Tracker, error) {
			return nil, errors.New("issue_tracker not configured")
		}
		p := mark(h, previews)

		if err := h.Run("status", "--workspace", branch, "--issue", "EX-12"); err != nil {
			t.Fatalf("status should tolerate a missing tracker; got %v", err)
		}

		if got := p.trackerCalls(); len(got) != 0 {
			t.Errorf("tracker consulted despite being unconfigured: %v", got)
		}
		if got := p.previewCalls(); len(got) != 0 {
			t.Errorf("preview consulted without a tracker at workspace scope: %v", got)
		}
		if n := countCall(p.hostCalls(), "GetPRForBranch"); n != 1 {
			t.Errorf("repos not probed without a tracker; GetPRForBranch = %d, want 1", n)
		}
		assertReadOnly(t, p)
	})

	t.Run("partial_data/project_scope_previews_without_tracker", func(t *testing.T) {
		// Project scope diverges from workspace scope here on purpose:
		// the issue key comes from the workspace *name*, so the preview
		// lookup doesn't need the tracker and still runs. Pinning the
		// asymmetry keeps a future "just gate both on the tracker"
		// simplification from silently dropping the Preview row.
		h, previews := newStatusHarness(t, "api")
		startWorkspace(t, h, "EX-13", "Trackerless project", "tp")
		cli.GetServices().IssueTracker = func() (issue.Tracker, error) {
			return nil, errors.New("issue_tracker not configured")
		}
		p := mark(h, previews)

		if err := h.Run("status"); err != nil {
			t.Fatalf("status should tolerate a missing tracker; got %v", err)
		}

		if got := p.trackerCalls(); len(got) != 0 {
			t.Errorf("tracker consulted despite being unconfigured: %v", got)
		}
		if n := countCall(p.previewCalls(), "Get"); n != 1 {
			t.Errorf("preview Get calls = %d, want 1 (issue key comes from the workspace name)", n)
		}
		assertReadOnly(t, p)
	})
}
