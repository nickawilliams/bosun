package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

// TestReleaseTargetVersionEligibility covers the pre-gate filter:
// version bump available, not already swept up, no lookup error. The
// release gate layers on top of this (TestClassifyReleaseGate /
// TestEligibleRequiresGate).
func TestReleaseTargetVersionEligibility(t *testing.T) {
	tests := []struct {
		name                string
		target              releaseTarget
		wantVersionEligible bool
		wantNote            string
	}{
		{
			name:                "bump from existing tag",
			target:              releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4"},
			wantVersionEligible: true,
			wantNote:            "v1.2.3",
		},
		{
			name:                "first release (no tags)",
			target:              releaseTarget{currentTag: "", nextVersion: "v0.1.0"},
			wantVersionEligible: true,
			wantNote:            "(none)",
		},
		{
			name:                "already at target version",
			target:              releaseTarget{currentTag: "v2.0.0", nextVersion: "v2.0.0"},
			wantVersionEligible: false,
			wantNote:            "v2.0.0",
		},
		{
			name: "containing release pins eligibility to false",
			// Even when nextVersion would be a bump, a release tag
			// containing our HEAD means we should NOT cut a new one.
			target: releaseTarget{
				currentTag:        "v1.2.3",
				nextVersion:       "v1.2.4",
				containingRelease: &code.Release{Tag: "v1.2.4", AuthorLogin: "alice"},
			},
			wantVersionEligible: false,
			wantNote:            "v1.2.3",
		},
		{
			name:                "lookup error",
			target:              releaseTarget{tagErr: errors.New("boom")},
			wantVersionEligible: false,
			wantNote:            "(none)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := tt.target
			if got := rt.versionEligible(); got != tt.wantVersionEligible {
				t.Errorf("versionEligible() = %v, want %v", got, tt.wantVersionEligible)
			}
			if got := rt.versionNote(); got != tt.wantNote {
				t.Errorf("versionNote() = %q, want %q", got, tt.wantNote)
			}
		})
	}
}

// TestProducesResult locks which repo states land a row in
// releaseResults — the predicate the notify action uses to decide
// whether there's anything to announce. Must match the release action's
// Assess/Apply branches that append results.
func TestProducesResult(t *testing.T) {
	tests := []struct {
		name string
		rt   releaseTarget
		want bool
	}{
		{"selected for release", releaseTarget{include: true, currentTag: "v1.2.3", nextVersion: "v1.2.4"}, true},
		{"sweep-up containing release", releaseTarget{containingRelease: &code.Release{Tag: "v1.2.4"}, currentTag: "v1.2.3", nextVersion: "v1.2.4"}, true},
		{"already at current version", releaseTarget{currentTag: "v2.0.0", nextVersion: "v2.0.0"}, true},
		{"gate-blocked (unmerged, would-release)", releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4", gate: gateBlock}, false},
		{"gate-skipped (nothing beyond default)", releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4", gate: gateSkip}, false},
		{"deselected eligible", releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4", include: false}, false},
		{"lookup error", releaseTarget{tagErr: errors.New("boom"), currentTag: "v1.2.3", nextVersion: "v1.2.4"}, false},
		{"first release, not selected", releaseTarget{currentTag: "", nextVersion: "v0.1.0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := tt.rt
			if got := rt.producesResult(); got != tt.want {
				t.Errorf("producesResult() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClassifyReleaseGate locks the pure gate decision: a merged PR is
// the only PR state that allows a release; an unmerged PR blocks with
// its state named; with no PR, the on-default ancestry check splits
// "nothing to release" (skip) from "unmerged work, no PR" (block).
func TestClassifyReleaseGate(t *testing.T) {
	tests := []struct {
		name            string
		prNumber        int
		prState         string
		branchOnDefault bool
		wantGate        releaseGate
		wantReason      string
	}{
		{"merged PR → allow", 42, "merged", false, gateAllow, ""},
		{"open PR → block", 42, "open", false, gateBlock, "PR #42 open — not merged"},
		{"draft PR → block", 42, "draft", false, gateBlock, "PR #42 draft — not merged"},
		{"closed unmerged PR → block", 42, "closed", false, gateBlock, "PR #42 closed — not merged"},
		// A merged PR wins regardless of the ancestry bool (never
		// consulted for the PR path — squash-merge would read false).
		{"merged PR ignores ancestry", 42, "merged", false, gateAllow, ""},
		{"no PR, on default → skip", 0, "", true, gateSkip, "no changes to release from this workspace"},
		{"no PR, not on default → block", 0, "", false, gateBlock, "no PR — work not on the default branch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate, reason := classifyReleaseGate(tt.prNumber, tt.prState, tt.branchOnDefault)
			if gate != tt.wantGate {
				t.Errorf("gate = %v, want %v", gate, tt.wantGate)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestApplyEmptyReleaseGuard locks the empty-release downgrade: an
// allowed merged repo whose default branch is already contained in the
// latest tag (shipped) becomes a skip; everything else passes through.
func TestApplyEmptyReleaseGuard(t *testing.T) {
	tests := []struct {
		name       string
		gate       releaseGate
		reason     string
		currentTag string
		shipped    bool
		wantGate   releaseGate
		wantReason string
	}{
		{"allow + shipped → skip", gateAllow, "", "v1.2.3", true, gateSkip, "already released — nothing new since v1.2.3"},
		{"allow + not shipped → allow", gateAllow, "", "v1.2.3", false, gateAllow, ""},
		{"block passes through even if shipped", gateBlock, "PR #42 open — not merged", "v1.2.3", true, gateBlock, "PR #42 open — not merged"},
		{"skip passes through", gateSkip, "no changes to release from this workspace", "v1.2.3", true, gateSkip, "no changes to release from this workspace"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gate, reason := applyEmptyReleaseGuard(tt.gate, tt.reason, tt.currentTag, tt.shipped)
			if gate != tt.wantGate {
				t.Errorf("gate = %v, want %v", gate, tt.wantGate)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestEligibleRequiresGate locks eligible() = versionEligible() AND
// gate == gateAllow: only an allowed, version-eligible repo is offered
// for release.
func TestEligibleRequiresGate(t *testing.T) {
	gateCases := []struct {
		name string
		gate releaseGate
		want bool
	}{
		{"allow → eligible", gateAllow, true},
		{"block → not eligible", gateBlock, false},
		{"skip → not eligible", gateSkip, false},
		{"unknown → not eligible", gateUnknown, false},
	}
	for _, c := range gateCases {
		t.Run(c.name, func(t *testing.T) {
			rt := releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4", gate: c.gate}
			if got := rt.eligible(); got != c.want {
				t.Errorf("eligible() = %v, want %v", got, c.want)
			}
			if got := rt.preselect(); got != c.want {
				t.Errorf("preselect() = %v, want %v", got, c.want)
			}
		})
	}

	// A version-ineligible target is never eligible, even with gateAllow.
	swept := releaseTarget{
		currentTag:        "v1.2.3",
		nextVersion:       "v1.2.4",
		containingRelease: &code.Release{Tag: "v1.2.4"},
		gate:              gateAllow,
	}
	if swept.eligible() {
		t.Error("swept-up target must not be eligible even with gateAllow")
	}
}

// TestDefaultSubjectsFor covers the seed-list policy that drives both
// applyDefaults (non-interactive) and the form's pre-check state. The
// rendered output of these defaults is locked separately by
// TestFormatSubjects, which collapses full-coverage selections back to
// the repo name.
func TestDefaultSubjectsFor(t *testing.T) {
	repo := Repository{Name: "monorepo", Path: "/tmp/monorepo"}

	tests := []struct {
		name string
		rt   releaseTarget
		want []string
	}{
		{
			name: "no services configured → [repo name]",
			rt:   releaseTarget{repo: repo, currentTag: "v1.0.0"},
			want: []string{"monorepo"},
		},
		{
			name: "single service → [that service]",
			rt:   releaseTarget{repo: repo, currentTag: "v1.0.0", services: []string{"api"}},
			want: []string{"api"},
		},
		{
			name: "detection narrowed → detected subset",
			rt: releaseTarget{
				repo: repo, currentTag: "v1.0.0",
				services:         []string{"api", "worker", "ui"},
				affectedServices: []string{"api", "ui"},
			},
			want: []string{"api", "ui"},
		},
		{
			name: "can't narrow (first release) → all services",
			rt: releaseTarget{
				repo: repo, currentTag: "",
				services:         []string{"api", "worker", "ui"},
				affectedServices: nil,
			},
			want: []string{"api", "worker", "ui"},
		},
		{
			name: "can't narrow (no path-map) → all services",
			rt: releaseTarget{
				repo: repo, currentTag: "v1.0.0",
				services:         []string{"api", "worker"},
				affectedServices: nil,
			},
			want: []string{"api", "worker"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := tt.rt
			got := defaultSubjectsFor(&rt)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFormatSubjects locks the display-collapsing rule: when the user's
// selection covers all configured services (or when no services are
// configured), the label collapses to the repo name to avoid a
// long-ass inline list.
func TestFormatSubjects(t *testing.T) {
	tests := []struct {
		name             string
		services         []string
		subjects         []string
		want             string
	}{
		{
			name:     "no subjects → repo name",
			services: []string{"api", "worker"},
			subjects: nil,
			want:     "monorepo",
		},
		{
			name:     "no services configured, single subject → repo name",
			services: nil,
			subjects: []string{"monorepo"},
			want:     "monorepo",
		},
		{
			name:     "partial selection → comma-joined",
			services: []string{"api", "worker", "ui"},
			subjects: []string{"api", "ui"},
			want:     "api`, `ui",
		},
		{
			name:     "all services selected → repo name (collapsed)",
			services: []string{"api", "worker"},
			subjects: []string{"api", "worker"},
			want:     "monorepo",
		},
		{
			name:     "single service repo with that service selected → repo name (collapsed)",
			services: []string{"api"},
			subjects: []string{"api"},
			want:     "monorepo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSubjects("monorepo", tt.services, tt.subjects, "`, `")
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompareSemverTag(t *testing.T) {
	tests := []struct {
		a, b string
		want int // -1, 0, +1 normalized to sign
	}{
		{"v0.4.582", "v0.4.583", -1}, // patch differs
		{"v0.4.583", "v0.4.582", 1},
		{"v0.4.583", "v0.4.583", 0},
		{"v1.0.0", "v0.99.99", 1},   // major dominates
		{"v0.5.0", "v0.4.999", 1},   // minor dominates
		{"v0.4.2", "v0.4.10", -1},   // numeric compare, not lexicographic
		{"0.4.5", "v0.4.5", 0},      // optional v prefix
		{"junk", "v1.0.0", 1},       // non-semver sorts after
		{"junk", "junk2", -1},       // both non-semver → string compare
	}
	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			got := compareSemverTag(tt.a, tt.b)
			// Normalize Compare-style result (any negative / any positive)
			// to the test's sign convention so out-of-the-ordinary returns
			// (e.g. strings.Compare can return -42) don't false-fail.
			sign := 0
			switch {
			case got < 0:
				sign = -1
			case got > 0:
				sign = 1
			}
			if sign != tt.want {
				t.Errorf("compareSemverTag(%q, %q) = %d, want sign %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestFormatExtrasNote(t *testing.T) {
	tests := []struct {
		name string
		prs  []code.PullRequest
		want string
	}{
		{
			name: "empty list → empty string",
			prs:  nil,
			want: "",
		},
		{
			name: "single PR",
			prs:  []code.PullRequest{{Number: 42, AuthorLogin: "alice"}},
			want: "also #42 @alice",
		},
		{
			name: "two PRs",
			prs: []code.PullRequest{
				{Number: 42, AuthorLogin: "alice"},
				{Number: 43, AuthorLogin: "bob"},
			},
			want: "also #42 @alice, #43 @bob",
		},
		{
			name: "three PRs inline",
			prs: []code.PullRequest{
				{Number: 42, AuthorLogin: "alice"},
				{Number: 43, AuthorLogin: "bob"},
				{Number: 44, AuthorLogin: "carol"},
			},
			want: "also #42 @alice, #43 @bob, #44 @carol",
		},
		{
			name: "more than three truncates with count",
			prs: []code.PullRequest{
				{Number: 42, AuthorLogin: "alice"},
				{Number: 43, AuthorLogin: "bob"},
				{Number: 44, AuthorLogin: "carol"},
				{Number: 45, AuthorLogin: "dave"},
				{Number: 46, AuthorLogin: "eve"},
			},
			want: "also #42 @alice, #43 @bob, #44 @carol, and 2 more",
		},
		{
			name: "PR without author renders just the number",
			prs:  []code.PullRequest{{Number: 50}},
			want: "also #50",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatExtrasNote(tt.prs)
			if got != tt.want {
				t.Errorf("formatExtrasNote() = %q, want %q", got, tt.want)
			}
		})
	}

	// Sanity: the truncated form preserves the "and N more" phrasing
	// regardless of truncation count.
	suffix := strings.HasSuffix(formatExtrasNote([]code.PullRequest{
		{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4},
	}), "and 1 more")
	if !suffix {
		t.Errorf("expected truncation suffix 'and 1 more'")
	}
}

func TestParseSubjectKey(t *testing.T) {
	tests := []struct {
		key       string
		wantRepo  int
		wantSvc   int
		wantOk    bool
	}{
		{"0.0", 0, 0, true},
		{"3.2", 3, 2, true},
		{"5.-1", 5, -1, true}, // fallback row marker (no services configured)
		{"7.-2", 7, -2, true}, // info-only row marker (containing-release; pick is ignored)
		{"", 0, 0, false},
		{"3", 0, 0, false},
		{"a.b", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			r, s, ok := parseSubjectKey(tt.key)
			if ok != tt.wantOk {
				t.Errorf("ok = %v, want %v", ok, tt.wantOk)
			}
			if ok && (r != tt.wantRepo || s != tt.wantSvc) {
				t.Errorf("got (%d, %d), want (%d, %d)", r, s, tt.wantRepo, tt.wantSvc)
			}
		})
	}
}

// TestCanMergePR locks which mergeable_state values are safe to auto-merge.
func TestCanMergePR(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"clean", true},
		{"has_hooks", true},
		{"blocked", false},
		{"unstable", false},
		{"dirty", false},
		{"behind", false},
		{"draft", false},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if got := canMergePR(tt.state); got != tt.want {
				t.Errorf("canMergePR(%q) = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// TestPRMergeBlockReason locks the short human reason an unmergeable PR
// shows, derived from mergeable_state + review decision.
func TestPRMergeBlockReason(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		review     string
		want       string
	}{
		{"conflicts", "dirty", "", "conflicts"},
		{"behind base", "behind", "", "behind base"},
		{"checks failing", "unstable", "", "checks failing"},
		{"draft", "draft", "", "draft"},
		{"blocked + changes requested", "blocked", "changes_requested", "changes requested"},
		{"blocked + approved → checks required", "blocked", "approved", "checks required"},
		{"blocked + awaiting", "blocked", "awaiting", "awaiting review"},
		{"blocked + no review", "blocked", "", "awaiting review"},
		{"unknown", "unknown", "", "mergeability unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prMergeBlockReason(tt.state, tt.review); got != tt.want {
				t.Errorf("prMergeBlockReason(%q, %q) = %q, want %q", tt.state, tt.review, got, tt.want)
			}
		})
	}
}
