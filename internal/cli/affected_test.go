package cli

import (
	"context"
	"errors"
	"slices"
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

// TestSourceRows locks the row renderer the gather and the record card
// share — the whole point of extracting it is that neither surface can
// describe a repo differently from the other.
func TestSourceRows(t *testing.T) {
	// strip flattens a repo's rows to "<glyph> <content>" lines.
	strip := func(rows []serviceRow) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = ansi.Strip(r.glyph) + " " + ansi.Strip(r.content)
		}
		return out
	}

	t.Run("multi-service repo pairs each service, name-ordered", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "api", HasChanges: true,
			Services: []string{"svc-c"}, Skipped: []string{"svc-b"}},
			pr: code.PullRequest{Number: 7}}

		got := strip(sourceRows(sr, true))
		want := []string{"○ api · svc-b", "✓ api · svc-c"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want the pairs sorted by service %q", got, want)
		}
	})

	t.Run("single-service repo shows the name alone", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "api", HasChanges: true,
			Services: []string{"api-svc"}}, pr: code.PullRequest{Number: 7}}

		got := strip(sourceRows(sr, true))
		want := []string{"✓ api"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want the bare repo name %q", got, want)
		}
	})

	t.Run("stale fetch rides above the repo's outcome row", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "cron", HasChanges: true,
			Services: []string{"jobs"}, StaleRemote: true}, pr: code.PullRequest{Number: 9}}

		rows := sourceRows(sr, true)
		got := strip(rows)
		if len(got) != 2 || !strings.Contains(got[0], "diff may be stale") {
			t.Fatalf("rows = %q, want the stale caveat first", got)
		}
		// The caveat is context about a repo that IS deploying — it
		// must not drag the card's aggregate toward skipped/failed.
		if rows[0].sev != rowNote {
			t.Errorf("stale row severity = %v, want rowNote (no aggregate contribution)", rows[0].sev)
		}
	})

	t.Run("unchanged repo collapses to one receded row", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "web",
			Skipped: []string{"web-svc", "worker"}}}

		got := strip(sourceRows(sr, true))
		want := []string{"○ web · no changes · no PR"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want one compact row %q", got, want)
		}
	})

	t.Run("changed repo without a PR warns and names the branch", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "docs", Branch: "feat",
			HasChanges: true, Services: []string{"site"}}}

		got := strip(sourceRows(sr, true))
		want := []string{`▲ docs · no PR for branch "feat"`}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want the warn row %q", got, want)
		}
	})

	t.Run("PR lookup failure renders as a failure row", func(t *testing.T) {
		sr := sourceRepo{res: AffectedResult{RepoName: "docs"}, prErr: errors.New("api down")}

		rows := sourceRows(sr, true)
		got := strip(rows)
		want := []string{"✗ docs · api down"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want the failure row %q", got, want)
		}
		if rows[0].sev != rowFail {
			t.Errorf("severity = %v, want rowFail", rows[0].sev)
		}
	})

	t.Run("the release path ignores PR state entirely", func(t *testing.T) {
		// withPRs=false: no PR, no lookup error to report — the
		// services detection found flow straight through.
		sr := sourceRepo{res: AffectedResult{RepoName: "api", HasChanges: true,
			Services: []string{"svc-a"}, Skipped: []string{"svc-b"}}}

		got := strip(sourceRows(sr, false))
		want := []string{"✓ api · svc-a", "○ api · svc-b"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want the pairs %q", got, want)
		}
	})
}

// TestBuildServicesCardOrdering locks the record card's ordering
// contract: repos in name order, each repo's rows exactly as its
// renderer produced them. The gather paints rows repo-by-repo in that
// same order, so any reshuffling here would make the form → card swap
// jump.
func TestBuildServicesCardOrdering(t *testing.T) {
	sources := []sourceRepo{
		{res: AffectedResult{RepoName: "web", HasChanges: true,
			Services: []string{"web-svc"}}, pr: code.PullRequest{Number: 3}},
		{res: AffectedResult{RepoName: "api", HasChanges: true, StaleRemote: true,
			Services: []string{"svc-b"}, Skipped: []string{"svc-a"}},
			pr: code.PullRequest{Number: 7}},
	}
	fails := []detFail{{repo: "ghost", err: errors.New("exploded")}}

	out := ansi.Strip(buildServicesCard(sources, fails, true).Render())
	// Item lines carry the card's timeline spine and column padding —
	// collapse both so the assertion reads as the row content it means.
	var got []string
	for line := range strings.SplitSeq(out, "\n") {
		row := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "│"))
		if row == "" || strings.HasSuffix(row, "Services") {
			continue
		}
		got = append(got, strings.Join(strings.Fields(row), " "))
	}
	want := []string{
		"▲ api · remote fetch failed — diff may be stale",
		"○ api · svc-a",
		"✓ api · svc-b",
		"✗ ghost · exploded",
		"✓ web",
	}
	if !slices.Equal(got, want) {
		t.Errorf("card rows =\n%q\nwant\n%q", got, want)
	}
}

// TestBuildServicesCardState locks the aggregate glyph: worst-first,
// with the stale-fetch caveat deliberately not counting — a deploying
// repo whose fetch failed is still a success row.
func TestBuildServicesCardState(t *testing.T) {
	deploying := sourceRepo{res: AffectedResult{RepoName: "api", HasChanges: true,
		Services: []string{"svc-a"}, StaleRemote: true}, pr: code.PullRequest{Number: 7}}
	unchanged := sourceRepo{res: AffectedResult{RepoName: "web"}}
	broken := sourceRepo{res: AffectedResult{RepoName: "docs"}, prErr: errors.New("api down")}

	tests := []struct {
		name    string
		sources []sourceRepo
		fails   []detFail
		want    string
	}{
		{"a deploying repo with a stale fetch stays success", []sourceRepo{deploying}, nil, "✓"},
		{"nothing deploying reads as skipped", []sourceRepo{unchanged}, nil, "▲"},
		{"a mix stays success", []sourceRepo{deploying, unchanged}, nil, "✓"},
		{"a PR-lookup failure dominates", []sourceRepo{deploying, broken}, nil, "✗"},
		{"a detection failure dominates", []sourceRepo{deploying}, []detFail{{repo: "ghost", err: errNope}}, "✗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := buildServicesCard(tt.sources, tt.fails, true).Render()
			if got := cardGlyph(t, out); got != tt.want {
				t.Errorf("card glyph = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnyPathMatches(t *testing.T) {
	tests := []struct {
		name     string
		changed  []string
		prefixes []string
		want     bool
	}{
		{
			name:     "directory prefix match",
			changed:  []string{"cmd/api/activity/handler.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     true,
		},
		{
			name:     "exact file match",
			changed:  []string{"go.mod"},
			prefixes: []string{"go.mod"},
			want:     true,
		},
		{
			name:     "no match",
			changed:  []string{"cmd/worker/main.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     false,
		},
		{
			name:     "prefix without trailing slash requires exact match",
			changed:  []string{"go.modx"},
			prefixes: []string{"go.mod"},
			want:     false,
		},
		{
			name:     "multiple changed files one matches",
			changed:  []string{"README.md", "cmd/api/activity/handler.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     true,
		},
		{
			name:     "multiple prefixes one matches",
			changed:  []string{"pkg/shared/util.go"},
			prefixes: []string{"cmd/api/", "pkg/shared/"},
			want:     true,
		},
		{
			name:     "empty changed files",
			changed:  nil,
			prefixes: []string{"cmd/api/"},
			want:     false,
		},
		{
			name:     "empty prefixes",
			changed:  []string{"cmd/api/main.go"},
			prefixes: nil,
			want:     false,
		},
		{
			name:     "nested directory match",
			changed:  []string{"pkg/auth/jwt/token.go"},
			prefixes: []string{"pkg/"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyPathMatches(tt.changed, tt.prefixes)
			if got != tt.want {
				t.Errorf("anyPathMatches(%v, %v) = %v, want %v",
					tt.changed, tt.prefixes, got, tt.want)
			}
		})
	}
}

func TestMatchServicePaths(t *testing.T) {
	services := []string{"activity-api", "admin-api", "worker"}
	pathMap := map[string][]string{
		"activity-api": {"cmd/api/activity/"},
		"admin-api":    {"cmd/api/admin/"},
		"worker":       {"cmd/worker/"},
		"_shared":      {"go.mod", "go.sum", "pkg/"},
	}

	t.Run("single service affected", func(t *testing.T) {
		changed := []string{"cmd/api/activity/handler.go", "cmd/api/activity/routes.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 1 || result.Services[0] != "activity-api" {
			t.Errorf("Services = %v, want [activity-api]", result.Services)
		}
		if len(result.Skipped) != 2 {
			t.Errorf("Skipped = %v, want 2 entries", result.Skipped)
		}
	})

	t.Run("shared trigger includes all", func(t *testing.T) {
		changed := []string{"go.mod"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 3 {
			t.Errorf("Services = %v, want all 3", result.Services)
		}
		if len(result.Skipped) != 0 {
			t.Errorf("Skipped = %v, want empty", result.Skipped)
		}
	})

	t.Run("shared pkg prefix includes all", func(t *testing.T) {
		changed := []string{"pkg/auth/token.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 3 {
			t.Errorf("Services = %v, want all 3", result.Services)
		}
	})

	t.Run("no matching paths", func(t *testing.T) {
		changed := []string{"README.md", ".github/workflows/ci.yml"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if result.HasChanges {
			t.Error("HasChanges should be false")
		}
		if len(result.Services) != 0 {
			t.Errorf("Services = %v, want empty", result.Services)
		}
		if len(result.Skipped) != 3 {
			t.Errorf("Skipped = %v, want all 3", result.Skipped)
		}
	})

	t.Run("multiple services affected", func(t *testing.T) {
		changed := []string{"cmd/api/activity/handler.go", "cmd/worker/main.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 2 {
			t.Errorf("Services = %v, want 2", result.Services)
		}
		if len(result.Skipped) != 1 {
			t.Errorf("Skipped = %v, want 1", result.Skipped)
		}
	})

	t.Run("service without path config included conservatively", func(t *testing.T) {
		servicesWithExtra := []string{"activity-api", "admin-api", "worker", "unmapped-svc"}
		changed := []string{"cmd/api/activity/handler.go"}
		result := matchServicePaths("extracker", servicesWithExtra, changed, pathMap)

		// unmapped-svc has no entry in pathMap → included conservatively.
		found := false
		for _, s := range result.Services {
			if s == "unmapped-svc" {
				found = true
			}
		}
		if !found {
			t.Errorf("Services = %v, want unmapped-svc included", result.Services)
		}
	})
}
