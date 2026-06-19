package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

func TestReleaseTargetClassification(t *testing.T) {
	tests := []struct {
		name          string
		target        releaseTarget
		wantEligible  bool
		wantPreselect bool
		wantNote      string
	}{
		{
			name:          "bump from existing tag",
			target:        releaseTarget{currentTag: "v1.2.3", nextVersion: "v1.2.4"},
			wantEligible:  true,
			wantPreselect: true,
			wantNote:      "v1.2.3",
		},
		{
			name:          "first release (no tags)",
			target:        releaseTarget{currentTag: "", nextVersion: "v0.1.0"},
			wantEligible:  true,
			wantPreselect: true,
			wantNote:      "(none)",
		},
		{
			name:          "already at target version",
			target:        releaseTarget{currentTag: "v2.0.0", nextVersion: "v2.0.0"},
			wantEligible:  false,
			wantPreselect: false,
			wantNote:      "v2.0.0",
		},
		{
			name: "containing release pins eligibility to false",
			// Even when nextVersion would be a bump, a release tag
			// containing our HEAD means we should NOT cut a new one
			// — eligible() flips to false; the notify path handles
			// the existing release.
			target: releaseTarget{
				currentTag:        "v1.2.3",
				nextVersion:       "v1.2.4",
				containingRelease: &code.Release{Tag: "v1.2.4", AuthorLogin: "alice"},
			},
			wantEligible:  false,
			wantPreselect: false,
			wantNote:      "v1.2.3",
		},
		{
			name:          "lookup error",
			target:        releaseTarget{tagErr: errors.New("boom")},
			wantEligible:  false,
			wantPreselect: false,
			wantNote:      "(none)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := tt.target
			if got := rt.eligible(); got != tt.wantEligible {
				t.Errorf("eligible() = %v, want %v", got, tt.wantEligible)
			}
			if got := rt.preselect(); got != tt.wantPreselect {
				t.Errorf("preselect() = %v, want %v", got, tt.wantPreselect)
			}
			if got := rt.versionNote(); got != tt.wantNote {
				t.Errorf("versionNote() = %q, want %q", got, tt.wantNote)
			}
		})
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
