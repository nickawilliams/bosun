package cli

import (
	"errors"
	"reflect"
	"testing"
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

func TestParseSubjectKey(t *testing.T) {
	tests := []struct {
		key       string
		wantRepo  int
		wantSvc   int
		wantOk    bool
	}{
		{"0.0", 0, 0, true},
		{"3.2", 3, 2, true},
		{"5.-1", 5, -1, true}, // fallback row marker
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
