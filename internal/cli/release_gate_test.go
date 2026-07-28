package cli

import (
	"context"
	"testing"
)

// TestClassifyServiceDeploy locks the per-service production decision:
// no containing release blocks; an unmerged/undeterminable deployed
// state deploys permissively; a deployed tag at or past the containing
// tag skips (already live / would roll back); behind deploys.
func TestClassifyServiceDeploy(t *testing.T) {
	tests := []struct {
		name          string
		containingTag string
		deployedTag   string
		deployedKnown bool
		wantState     deployState
		wantReason    string
	}{
		{"no release contains work → block", "", "", true, deployBlock, "no release contains this work — run prerelease"},
		{"no release wins over deployed → block", "", "v1.2.3", true, deployBlock, "no release contains this work — run prerelease"},
		{"unknown deployed → go (permissive)", "v1.2.4", "", false, deployGo, "→ v1.2.4 (deployed state unknown)"},
		{"never deployed → first deploy", "v1.2.4", "", true, deployGo, "→ v1.2.4 (first deploy)"},
		{"behind → deploy", "v1.2.4", "v1.2.3", true, deployGo, "v1.2.3 → v1.2.4"},
		{"equal → skip", "v1.2.4", "v1.2.4", true, deploySkip, "v1.2.4 (already live)"},
		{"deployed newer → skip (rollback guard)", "v1.2.4", "v1.2.5", true, deploySkip, "v1.2.5 (already live)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reason := classifyServiceDeploy(tt.containingTag, tt.deployedTag, tt.deployedKnown)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestLowestContainingReleaseTag locks the release-tag selection: filter
// to release-shaped tags, take the lowest semver (first shipped).
func TestLowestContainingReleaseTag(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{"lowest of two", []string{"v1.2.3", "v1.2.4"}, "v1.2.3"},
		{"unsorted input", []string{"v1.2.4", "v1.2.3", "v1.3.0"}, "v1.2.3"},
		{"filters non-release tags", []string{"main", "not-a-tag", "v2.0.0"}, "v2.0.0"},
		{"numeric not lexicographic", []string{"v1.2.10", "v1.2.2"}, "v1.2.2"},
		{"none release-shaped", []string{"feature-x", "latest"}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lowestContainingReleaseTag(tt.tags); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseServiceDeployValue locks config parsing across the two
// granularities: a string value → one single-service target (env
// "production"); a per-service map → one target each, env defaulting to
// "<service>-production" unless explicitly overridden.
func TestParseServiceDeployValue(t *testing.T) {
	ctx := context.Background()
	repo := Repository{Name: "extracker", Path: "/tmp/extracker"}

	// String form (single-service repo).
	single, err := parseServiceDeployValue(ctx, repo, "host-ui", "ExtrackerInc/host-ui/.github/workflows/production.yml")
	if err != nil {
		t.Fatalf("string form error: %v", err)
	}
	if len(single) != 1 {
		t.Fatalf("string form: got %d targets, want 1", len(single))
	}
	if s := single[0]; s.Service != "host-ui" || s.Environment != "production" ||
		s.Owner != "ExtrackerInc" || s.Repo != "host-ui" || s.Workflow != "production.yml" {
		t.Errorf("string target = %+v", s)
	}

	// Map form (monorepo): default env vs explicit override.
	multi, err := parseServiceDeployValue(ctx, repo, "extracker", map[string]any{
		"account-api": map[string]any{
			"workflow": "ExtrackerInc/extracker/.github/workflows/api-account-production.yml",
		},
		"timecard-sync": map[string]any{
			"workflow":    "ExtrackerInc/extracker/.github/workflows/timecard-sync-production.yml",
			"environment": "hcss-sync-production",
		},
	})
	if err != nil {
		t.Fatalf("map form error: %v", err)
	}
	if len(multi) != 2 {
		t.Fatalf("map form: got %d targets, want 2", len(multi))
	}
	byService := map[string]DeployTarget{}
	for _, m := range multi {
		byService[m.Service] = m
	}
	if got := byService["account-api"].Environment; got != "account-api-production" {
		t.Errorf("account-api env = %q, want account-api-production (default)", got)
	}
	if got := byService["account-api"].Workflow; got != "api-account-production.yml" {
		t.Errorf("account-api workflow = %q", got)
	}
	if got := byService["timecard-sync"].Environment; got != "hcss-sync-production" {
		t.Errorf("timecard-sync env = %q, want hcss-sync-production (override)", got)
	}
	if lbl := byService["account-api"].Label; lbl != "extracker · account-api" {
		t.Errorf("label = %q, want 'extracker · account-api'", lbl)
	}
}
