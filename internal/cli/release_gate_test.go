package cli

import (
	"context"
	"testing"
)

// TestClassifyServiceDeploy locks the per-service production decision:
// no containing release blocks (the gate, keyed on workTag); an
// undeterminable deployed state deploys permissively; a deployed tag at
// or past the deploy tag skips (already live / would roll back); behind
// deploys. workTag gates, deployTag ships.
func TestClassifyServiceDeploy(t *testing.T) {
	tests := []struct {
		name          string
		workTag       string
		deployTag     string
		deployedTag   string
		deployedKnown bool
		explicit      bool
		wantState     deployState
		wantReason    string
	}{
		{"no release contains work → block", "", "v1.2.5", "", true, false, deployBlock, "no release contains this work — run prerelease"},
		{"no release wins over deployed → block", "", "v1.2.5", "v1.2.3", true, false, deployBlock, "no release contains this work — run prerelease"},
		{"unknown deployed → go (permissive)", "v1.2.4", "v1.2.5", "", false, false, deployGo, "→ v1.2.5 (deployed state unknown)"},
		{"never deployed → first deploy", "v1.2.4", "v1.2.5", "", true, false, deployGo, "→ v1.2.5 (first deploy)"},
		{"behind → deploy", "v1.2.4", "v1.2.5", "v1.2.3", true, false, deployGo, "v1.2.3 → v1.2.5"},
		{"equal to deploy tag → skip", "v1.2.4", "v1.2.5", "v1.2.5", true, false, deploySkip, "v1.2.5 (already live)"},
		{"deployed newer → skip (rollback guard)", "v1.2.4", "v1.2.5", "v1.2.6", true, false, deploySkip, "v1.2.6 (already live)"},
		// D at the work's release but behind the deploy tag → still deploys.
		{"at workTag, behind deployTag → deploy", "v1.2.4", "v1.2.5", "v1.2.4", true, false, deployGo, "v1.2.4 → v1.2.5"},
		// An explicit --tag naming an older release is a rollback
		// request — the operator gets what they asked for, labeled.
		{"explicit --tag older than deployed → rollback deploy", "v1.2.4", "v1.2.3", "v1.2.6", true, true, deployGo, "v1.2.6 → v1.2.3 (rollback, --tag)"},
		// Explicit but identical to what's live: still nothing to do.
		{"explicit --tag already live → skip", "v1.2.4", "v1.2.6", "v1.2.6", true, true, deploySkip, "v1.2.6 (already live)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reason := classifyServiceDeploy(tt.workTag, tt.deployTag, tt.deployedTag, tt.deployedKnown, tt.explicit)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestChooseDeployTag locks the deploy-tag choice: --tag override wins;
// else the workspace's own release — NOT the repo's latest (tags cut
// after ours are other people's deploys to coordinate, not absorb).
func TestChooseDeployTag(t *testing.T) {
	tests := []struct {
		name              string
		workTag, override string
		want              string
	}{
		{"override wins", "v1.2.4", "v1.2.2", "v1.2.2"},
		{"own release by default", "v1.2.4", "", "v1.2.4"},
		{"no work release, override still wins", "", "v1.2.5", "v1.2.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseDeployTag(tt.workTag, tt.override); got != tt.want {
				t.Errorf("chooseDeployTag(%q, %q) = %q, want %q", tt.workTag, tt.override, got, tt.want)
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

// TestRepoNoopDetail locks the repo-level rollup row that replaces
// per-service no-op itemization: gate block is the load-bearing why;
// any unselected deployable → "not selected"; else already live.
func TestRepoNoopDetail(t *testing.T) {
	mk := func(repo string, state deployState, workTag, reason string) releaseServiceTarget {
		return releaseServiceTarget{
			target:  DeployTarget{RepoName: repo},
			state:   state,
			workTag: workTag,
			reason:  reason,
		}
	}
	tests := []struct {
		name   string
		states []releaseServiceTarget
		want   string
	}{
		{
			"unselected deployables → not selected",
			[]releaseServiceTarget{
				mk("extracker", deployGo, "v1.2.4", "v1.2.3 → v1.2.4"),
				mk("extracker", deploySkip, "v1.2.4", "v1.2.4 (already live)"),
			},
			"v1.2.4 (not selected)",
		},
		{
			"all already live",
			[]releaseServiceTarget{
				mk("extracker", deploySkip, "v1.2.4", "v1.2.4 (already live)"),
			},
			"v1.2.4 (already live)",
		},
		{
			"all blocked → gate reason, no tag",
			[]releaseServiceTarget{
				mk("extracker", deployBlock, "", "no release contains this work — run prerelease"),
			},
			"(no release contains this work — run prerelease)",
		},
		{
			"other repos ignored",
			[]releaseServiceTarget{
				mk("host-ui", deployGo, "v9.9.9", ""),
				mk("extracker", deploySkip, "v1.2.4", ""),
			},
			"v1.2.4 (already live)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoNoopDetail(tt.states, "extracker"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAdvanceReleaseStatus locks the done-transition rollup: only a
// real deploy — or a fully-observed "nothing needed doing" — may move
// the issue, and failure to observe never reads as "nothing to do".
func TestAdvanceReleaseStatus(t *testing.T) {
	st := func(state deployState, include bool) releaseServiceTarget {
		return releaseServiceTarget{state: state, include: include}
	}

	tests := []struct {
		name     string
		observed bool
		states   []releaseServiceTarget
		want     bool
	}{
		{
			name:     "deploy selected → advance",
			observed: true,
			states:   []releaseServiceTarget{st(deployGo, true), st(deploySkip, false)},
			want:     true,
		},
		{
			name:     "everything already live → advance",
			observed: true,
			states:   []releaseServiceTarget{st(deploySkip, false), st(deploySkip, false)},
			want:     true,
		},
		{
			name:     "zero configured targets, providers fine → advance",
			observed: true,
			states:   nil,
			want:     true,
		},
		{
			name:     "providers failed to construct → hold",
			observed: false,
			states:   nil,
			want:     false,
		},
		{
			name:     "deployable but deselected → hold",
			observed: true,
			states:   []releaseServiceTarget{st(deployGo, false)},
			want:     false,
		},
		{
			name:     "blocked-only work → hold",
			observed: true,
			states:   []releaseServiceTarget{st(deployBlock, false), st(deploySkip, false)},
			want:     false,
		},
		{
			name:     "erred target → hold even with skips",
			observed: true,
			states: []releaseServiceTarget{
				{err: fakeErr("resolve failed")},
				st(deploySkip, false),
			},
			want: false,
		},
		{
			name:     "erred target → hold even alongside a deploy",
			observed: true,
			states: []releaseServiceTarget{
				st(deployGo, true),
				{err: fakeErr("resolve failed")},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := advanceReleaseStatus(tt.observed, tt.states); got != tt.want {
				t.Errorf("advanceReleaseStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
