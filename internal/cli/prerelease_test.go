package cli

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/spf13/viper"
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

func TestSubjectsForRelease(t *testing.T) {
	repo := Repository{Name: "monorepo", Path: "/tmp/monorepo"}

	t.Run("repo with no services configured → repo name", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		rt := &releaseTarget{repo: repo, currentTag: "v1.0.0"}
		got := subjectsForRelease(context.Background(), nil, rt)
		want := []string{"monorepo"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("single-service repo → that service", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		viper.Set("services.monorepo", "api")
		rt := &releaseTarget{repo: repo, currentTag: "v1.0.0"}
		got := subjectsForRelease(context.Background(), nil, rt)
		want := []string{"api"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("first release (no previous tag) → repo name", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		viper.Set("services.monorepo", []string{"api", "worker", "ui"})
		rt := &releaseTarget{repo: repo, currentTag: ""}
		got := subjectsForRelease(context.Background(), nil, rt)
		want := []string{"monorepo"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("multi-service repo without path-map → repo name", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		// List form (no per-service paths) — can't narrow.
		viper.Set("services.monorepo", []string{"api", "worker"})
		rt := &releaseTarget{repo: repo, currentTag: "v1.0.0"}
		got := subjectsForRelease(context.Background(), nil, rt)
		want := []string{"monorepo"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}
