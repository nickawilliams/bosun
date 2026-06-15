package cli

import (
	"errors"
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
