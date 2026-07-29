package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// affVCS fakes the three VCS calls change detection makes.
type affVCS struct {
	vcs.VCS
	defaultBranch string
	defaultErr    error
	fetchErr      error
	changed       []string
	changedErr    error
}

func (f affVCS) GetDefaultBranch(context.Context, string) (string, error) {
	return f.defaultBranch, f.defaultErr
}
func (f affVCS) Fetch(context.Context, string, string, string) error { return f.fetchErr }
func (f affVCS) ChangedFiles(context.Context, string, string) ([]string, error) {
	return f.changed, f.changedErr
}

// affSetup wires raw output, a services config for repo "api", and a
// code-host factory whose host is never reached (identity resolution
// fails first in these tests — the repo paths aren't git repos).
func affSetup(t *testing.T) *cobra.Command {
	t.Helper()
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	t.Cleanup(func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	})

	viper.Set("services.api", "svc-a")
	t.Cleanup(viper.Reset)

	prevServices := GetServices()
	t.Cleanup(func() { SetServices(prevServices) })
	svcs := *prevServices
	svcs.CodeHost = func() (code.Host, error) { return &seamHost{}, nil }
	SetServices(&svcs)

	cmd := &cobra.Command{Use: "t"}
	cmd.Flags().StringSlice("service", nil, "")
	return cmd
}

// TestEmitDeploymentSourcesNoPRFold locks the phase-3 contract from
// the no-PR finding: a changed repo whose PR can't be resolved is
// rendered not-deployable, so its services must fold into Skipped in
// the returned results — not ride into the deploy set with no pr-N
// override (regression: they deployed on the provider's default tag).
func TestEmitDeploymentSourcesNoPRFold(t *testing.T) {
	cmd := affSetup(t)
	g := affVCS{defaultBranch: "main", changed: []string{"svc-a/main.go"}}
	repos := []Repository{{Name: "api", Path: t.TempDir()}} // not a git repo → identity fails

	results, overrides, prs, err := emitDeploymentSources(
		context.Background(), cmd, g, repos, map[string]string{"api": "feature-x"}, true)
	if err != nil {
		t.Fatalf("emitDeploymentSources: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want the one repo", results)
	}
	if len(results[0].Services) != 0 {
		t.Errorf("Services = %v, want empty — unresolved-PR services must not deploy", results[0].Services)
	}
	if len(results[0].Skipped) == 0 || results[0].Skipped[0] != "svc-a" {
		t.Errorf("Skipped = %v, want the folded service", results[0].Skipped)
	}
	if len(overrides) != 0 || len(prs) != 0 {
		t.Errorf("overrides/prs = %v/%v, want none without a PR", overrides, prs)
	}
}

// TestEmitDeploymentSourcesWithoutPRs locks the release-path shape:
// withPRs=false needs no identities or overrides, and detection's
// services flow straight through.
func TestEmitDeploymentSourcesWithoutPRs(t *testing.T) {
	cmd := affSetup(t)
	g := affVCS{defaultBranch: "main", changed: []string{"svc-a/main.go"}}
	repos := []Repository{{Name: "api", Path: t.TempDir()}}

	results, overrides, _, err := emitDeploymentSources(
		context.Background(), cmd, g, repos, map[string]string{"api": "feature-x"}, false)
	if err != nil {
		t.Fatalf("emitDeploymentSources: %v", err)
	}
	if len(results) != 1 || len(results[0].Services) != 1 || results[0].Services[0] != "svc-a" {
		t.Fatalf("results = %+v, want svc-a deploying", results)
	}
	if len(overrides) != 0 {
		t.Errorf("overrides = %v, want none on the release path", overrides)
	}
}

// TestEmitDeploymentSourcesDetectionError locks the error contract:
// a repo whose change detection fails renders a ✗ row and the error
// comes back to the caller (preview aborts on it under -y).
func TestEmitDeploymentSourcesDetectionError(t *testing.T) {
	cmd := affSetup(t)
	g := affVCS{defaultBranch: "main", changedErr: errors.New("boom")}
	repos := []Repository{{Name: "api", Path: t.TempDir()}}

	results, _, _, err := emitDeploymentSources(
		context.Background(), cmd, g, repos, map[string]string{"api": "feature-x"}, false)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the detection error surfaced", err)
	}
	if len(results) != 0 {
		t.Errorf("results = %+v, want none for a failed repo", results)
	}
}

// TestEmitDeploymentSourcesUnchangedRepo locks the no-changes shape:
// everything sits in Skipped, nothing deploys.
func TestEmitDeploymentSourcesUnchangedRepo(t *testing.T) {
	cmd := affSetup(t)
	g := affVCS{defaultBranch: "main"}
	repos := []Repository{{Name: "api", Path: t.TempDir()}}

	results, _, _, err := emitDeploymentSources(
		context.Background(), cmd, g, repos, map[string]string{"api": "feature-x"}, false)
	if err != nil {
		t.Fatalf("emitDeploymentSources: %v", err)
	}
	if len(results) != 1 || results[0].HasChanges || len(results[0].Services) != 0 {
		t.Fatalf("results = %+v, want an unchanged repo with no deploying services", results)
	}
}

// TestPRResolved locks the deployability predicate: withPRs requires
// a successful lookup AND an existing PR; the release path (no PRs
// needed) always resolves. HasChanges deliberately doesn't factor in
// (unchanged repos stay selectable for redeploys).
func TestPRResolved(t *testing.T) {
	tests := []struct {
		name    string
		sr      sourceRepo
		withPRs bool
		want    bool
	}{
		{"withPRs, PR present", sourceRepo{pr: code.PullRequest{Number: 7}}, true, true},
		{"withPRs, no PR", sourceRepo{}, true, false},
		{"withPRs, lookup failed", sourceRepo{prErr: errors.New("x")}, true, false},
		{"release path ignores PRs", sourceRepo{prErr: errors.New("x")}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sr.prResolved(tt.withPRs); got != tt.want {
				t.Errorf("prResolved(%v) = %v, want %v", tt.withPRs, got, tt.want)
			}
		})
	}
}

// TestBuildServicesCard locks the severity matrix of the final
// Services card: deploying rows ✓, excluded/unchanged rows receded,
// no-PR rows ▲ with the branch named, detection failures ✗, stale
// fetches noted — and the card state tracking the worst row.
func TestBuildServicesCard(t *testing.T) {
	sources := []sourceRepo{
		{res: AffectedResult{RepoName: "api", HasChanges: true,
			Services: []string{"svc-a"}, Skipped: []string{"svc-b"}},
			pr: code.PullRequest{Number: 7}},
		{res: AffectedResult{RepoName: "web", HasChanges: false}},
		{res: AffectedResult{RepoName: "docs", Branch: "feat", HasChanges: true,
			Services: []string{"site"}}}, // no PR → warn
		{res: AffectedResult{RepoName: "cron", HasChanges: true,
			Services: []string{"jobs"}, StaleRemote: true},
			pr: code.PullRequest{Number: 9}},
	}
	fails := []detFail{{repo: "ghost", err: errors.New("exploded")}}

	out := ansi.Strip(buildServicesCard(sources, fails, true).Render())

	for _, want := range []string{
		"svc-a",             // deploying pair
		"no changes",        // unchanged recede
		`no PR for branch`,  // warn row names the gap
		"exploded",          // detection failure row
		"diff may be stale", // stale-fetch note
	} {
		if !strings.Contains(out, want) {
			t.Errorf("card missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "✗") {
		t.Errorf("card missing the failure glyph:\n%s", out)
	}
}
