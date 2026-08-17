package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
)

func TestResolveRepoCardState(t *testing.T) {
	cases := []struct {
		name        string
		branchState string
		pr          code.PullRequest
		checks      code.CheckRollup
		want        ui.CardState
	}{
		// Terminal PR states — branch state is ignored.
		{name: "merged → done", pr: code.PullRequest{State: "merged"}, want: ui.CardSuccess},
		{name: "merged + diverged → done (terminal wins)", branchState: "diverged 1/2", pr: code.PullRequest{State: "merged"}, want: ui.CardSuccess},
		{name: "closed → broken", pr: code.PullRequest{State: "closed"}, want: ui.CardFailed},
		{name: "closed + behind → broken (terminal wins)", branchState: "behind 5", pr: code.PullRequest{State: "closed"}, want: ui.CardFailed},
		// Branch problems beat non-terminal PR mergeability.
		{name: "diverged + open clean → blocked (branch wins)", branchState: "diverged 1/2", pr: code.PullRequest{State: "open", MergeableState: "clean"}, want: ui.CardSkipped},
		{name: "behind + open clean → blocked (branch wins)", branchState: "behind 3", pr: code.PullRequest{State: "open", MergeableState: "clean"}, want: ui.CardSkipped},
		// PR mergeability for non-terminal PRs without branch problems.
		{name: "open + clean → ready", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "clean"}, want: ui.CardReady},
		{name: "open + dirty → blocked", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "dirty"}, want: ui.CardSkipped},
		{name: "open + behind → blocked", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "behind"}, want: ui.CardSkipped},
		{name: "open + blocked → blocked", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "blocked"}, want: ui.CardSkipped},
		{name: "open + unstable → blocked", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "unstable"}, want: ui.CardSkipped},
		// Blocked-but-waiting-on-others → pending, not blocked.
		{name: "open + blocked + awaiting review → pending", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "blocked", Review: "awaiting"}, want: ui.CardWaiting},
		{name: "open + blocked + checks running → pending", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "blocked"}, checks: code.CheckRollup{State: "running", Running: 1}, want: ui.CardWaiting},
		{name: "open + blocked + awaiting review + checks failing → blocked", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "blocked", Review: "awaiting"}, checks: code.CheckRollup{State: "failing", Failing: 1}, want: ui.CardSkipped},
		{name: "open + blocked + approved → blocked (review isn't the blocker)", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "blocked", Review: "approved"}, want: ui.CardSkipped},
		{name: "diverged + blocked + awaiting review → blocked (branch wins)", branchState: "diverged 1/2", pr: code.PullRequest{State: "open", MergeableState: "blocked", Review: "awaiting"}, want: ui.CardSkipped},
		{name: "open + unknown → pending", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "unknown"}, want: ui.CardWaiting},
		{name: "open + empty mergeable → pending", branchState: "in sync", pr: code.PullRequest{State: "open"}, want: ui.CardWaiting},
		// Defaults.
		{name: "draft → pending", branchState: "in sync", pr: code.PullRequest{State: "draft"}, want: ui.CardWaiting},
		{name: "no PR → pending", branchState: "in sync", pr: code.PullRequest{}, want: ui.CardWaiting},
		{name: "no PR + unpushed → pending", branchState: "unpushed 3", pr: code.PullRequest{}, want: ui.CardWaiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRepoCardState(tc.branchState, tc.pr, tc.checks); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBranchStateString(t *testing.T) {
	cases := []struct {
		name string
		sync vcs.BranchSync
		want string
	}{
		{name: "no remote zero ahead", sync: vcs.BranchSync{HasRemote: false, Ahead: 0}, want: "unpushed 0"},
		{name: "no remote with ahead", sync: vcs.BranchSync{HasRemote: false, Ahead: 3}, want: "unpushed 3"},
		{name: "in sync", sync: vcs.BranchSync{HasRemote: true}, want: "in sync"},
		{name: "ahead only", sync: vcs.BranchSync{HasRemote: true, Ahead: 2}, want: "ahead 2"},
		{name: "behind only", sync: vcs.BranchSync{HasRemote: true, Behind: 5}, want: "behind 5"},
		{name: "diverged", sync: vcs.BranchSync{HasRemote: true, Ahead: 1, Behind: 3}, want: "diverged 1/3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchStateString(tc.sync); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusBranchGlyph(t *testing.T) {
	cases := []struct {
		name      string
		sync      vcs.BranchSync
		wantGlyph string
		wantColor any // checked via the palette getter
	}{
		{name: "in sync", sync: vcs.BranchSync{HasRemote: true}, wantGlyph: "●  "},
		{name: "ahead 2", sync: vcs.BranchSync{HasRemote: true, Ahead: 2}, wantGlyph: "↑2 "},
		{name: "behind 5", sync: vcs.BranchSync{HasRemote: true, Behind: 5}, wantGlyph: "↓5 "},
		{name: "diverged sums", sync: vcs.BranchSync{HasRemote: true, Ahead: 1, Behind: 3}, wantGlyph: "↕4 "},
		{name: "diverged caps at 9+", sync: vcs.BranchSync{HasRemote: true, Ahead: 7, Behind: 8}, wantGlyph: "↕9+"},
		{name: "unpushed 3", sync: vcs.BranchSync{HasRemote: false, Ahead: 3}, wantGlyph: "+3 "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, _ := statusBranchGlyph(tc.sync)
			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}
		})
	}
}

func TestCountToken(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{n: -1, want: "  "},
		{n: 0, want: "  "},
		{n: 1, want: "1 "},
		{n: 9, want: "9 "},
		{n: 10, want: "9+"},
		{n: 100, want: "9+"},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			if got := countToken(tc.n); got != tc.want {
				t.Errorf("countToken(%d) = %q, want %q", tc.n, got, tc.want)
			}
		})
	}
}

func TestStatusPRDominant(t *testing.T) {
	cases := []struct {
		name        string
		state       string
		mergeState  string
		reviewState string
		checks      code.CheckRollup
		wantLabel   string
		wantGlyph   string
	}{
		// Terminal PR outcomes keep event-shaped glyphs per the grammar.
		{name: "merged → ✓ (terminal positive)", state: "merged", wantLabel: "merged", wantGlyph: "✓"},
		{name: "closed → ✗ (terminal negative)", state: "closed", wantLabel: "closed", wantGlyph: "✗"},
		// Draft is a state, not a terminal outcome.
		{name: "draft → ● (state)", state: "draft", wantLabel: "draft", wantGlyph: "●"},

		// Happy path: mergeable + approved.
		{name: "open + clean + approved → ●", state: "open", mergeState: "clean", reviewState: "approved", wantLabel: "approved", wantGlyph: "●"},
		{name: "open + clean + no review → ●", state: "open", mergeState: "clean", wantLabel: "open", wantGlyph: "●"},
		{name: "open + has_hooks + approved → ●", state: "open", mergeState: "has_hooks", reviewState: "approved", wantLabel: "approved", wantGlyph: "●"},

		// changes_requested wins over mergeable state — author needs to act regardless.
		{name: "open + clean + changes_requested → ●", state: "open", mergeState: "clean", reviewState: "changes_requested", wantLabel: "changes requested", wantGlyph: "●"},
		{name: "open + behind + changes_requested → ● (review wins)", state: "open", mergeState: "behind", reviewState: "changes_requested", wantLabel: "changes requested", wantGlyph: "●"},

		// Mergeable problems surface the specific blocker as a state.
		{name: "open + dirty + approved → ● conflicts (mergeability shadows approval)", state: "open", mergeState: "dirty", reviewState: "approved", wantLabel: "conflicts", wantGlyph: "●"},
		{name: "open + behind + approved → ● behind base", state: "open", mergeState: "behind", reviewState: "approved", wantLabel: "behind base", wantGlyph: "●"},
		{name: "open + unstable + approved → ● checks failing", state: "open", mergeState: "unstable", reviewState: "approved", wantLabel: "checks failing", wantGlyph: "●"},
		{name: "open + blocked + approved → ● blocked", state: "open", mergeState: "blocked", reviewState: "approved", wantLabel: "blocked", wantGlyph: "●"},

		// Blocked + checks running → in-flight (not attention).
		{name: "open + blocked + checks running → ● required checks pending", state: "open", mergeState: "blocked", checks: code.CheckRollup{State: "running", Running: 1}, wantLabel: "required checks pending", wantGlyph: "●"},
		{name: "open + blocked + checks running + approved → ● required checks pending", state: "open", mergeState: "blocked", reviewState: "approved", checks: code.CheckRollup{State: "running", Running: 2}, wantLabel: "required checks pending", wantGlyph: "●"},
		// changes_requested still wins over the running-checks pending state.
		{name: "open + blocked + checks running + changes_requested → ● changes requested", state: "open", mergeState: "blocked", reviewState: "changes_requested", checks: code.CheckRollup{State: "running"}, wantLabel: "changes requested", wantGlyph: "●"},
		// Blocked + failing checks → real block, attention.
		{name: "open + blocked + checks failing → ● blocked", state: "open", mergeState: "blocked", checks: code.CheckRollup{State: "failing", Failing: 1}, wantLabel: "blocked", wantGlyph: "●"},

		// Blocked + requested-but-unsubmitted review → the wait is on
		// the reviewer, not the user: in-flight with a specific label.
		{name: "open + blocked + awaiting → ● awaiting review", state: "open", mergeState: "blocked", reviewState: "awaiting", wantLabel: "awaiting review", wantGlyph: "●"},
		{name: "open + blocked + awaiting + checks passing → ● awaiting review", state: "open", mergeState: "blocked", reviewState: "awaiting", checks: code.CheckRollup{State: "passing", Passing: 3}, wantLabel: "awaiting review", wantGlyph: "●"},
		// Running checks stay the dominant pending signal; failing checks dominate the other way.
		{name: "open + blocked + awaiting + checks running → ● required checks pending", state: "open", mergeState: "blocked", reviewState: "awaiting", checks: code.CheckRollup{State: "running", Running: 1}, wantLabel: "required checks pending", wantGlyph: "●"},
		{name: "open + blocked + awaiting + checks failing → ● blocked", state: "open", mergeState: "blocked", reviewState: "awaiting", checks: code.CheckRollup{State: "failing", Failing: 1}, wantLabel: "blocked", wantGlyph: "●"},
		// No requested reviewer → asking one is on the user: still blocked.
		{name: "open + blocked + no review involved → ● blocked", state: "open", mergeState: "blocked", wantLabel: "blocked", wantGlyph: "●"},
		// Mergeable + review requested → specific label over generic "open".
		{name: "open + clean + awaiting → ● awaiting review", state: "open", mergeState: "clean", reviewState: "awaiting", wantLabel: "awaiting review", wantGlyph: "●"},

		// Indeterminate / pending states.
		{name: "open + unknown → ● unknown", state: "open", mergeState: "unknown", wantLabel: "unknown", wantGlyph: "●"},
		{name: "open + empty mergeable → ● open", state: "open", wantLabel: "open", wantGlyph: "●"},

		// No PR.
		{name: "no PR → empty label, ● glyph", wantLabel: "", wantGlyph: "●"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			label, _, glyph := statusPRDominant(tc.state, tc.mergeState, tc.reviewState, tc.checks)
			if label != tc.wantLabel {
				t.Errorf("label = %q, want %q", label, tc.wantLabel)
			}
			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}
		})
	}
}

func TestChecksSummary(t *testing.T) {
	cases := []struct {
		name   string
		rollup code.CheckRollup
		want   string
	}{
		{name: "all zero", rollup: code.CheckRollup{}, want: "(none)"},
		{name: "passing only", rollup: code.CheckRollup{Passing: 12}, want: "12 passing"},
		{name: "failing only", rollup: code.CheckRollup{Failing: 2}, want: "2 failing"},
		{name: "running only", rollup: code.CheckRollup{Running: 7}, want: "7 running"},
		{name: "passing + failing", rollup: code.CheckRollup{Passing: 10, Failing: 2}, want: "10 passing, 2 failing"},
		{name: "all three", rollup: code.CheckRollup{Passing: 5, Failing: 1, Running: 3}, want: "5 passing, 1 failing, 3 running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := checksSummary(tc.rollup); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusPreviewRow(t *testing.T) {
	cases := []struct {
		name       string
		env        preview.Environment
		err        error
		wantGlyph  string
		wantInVal  string // substring expected in the value, "" → expect empty result
		wantEmpty  bool
		wantSuffix string // optional substring expected at the suffix end (e.g., "(unverified)")
	}{
		{name: "no env bound → skip", err: preview.ErrNoEnvironment, wantEmpty: true},
		{name: "other error with no name → skip", err: errors.New("boom"), wantEmpty: true},
		{name: "alive → ● + name", env: preview.Environment{Name: "brave-falcon", URL: "https://x", Probed: true, Alive: true}, wantGlyph: "●  ", wantInVal: "brave-falcon"},
		{name: "probed dead → ● + unreachable", env: preview.Environment{Name: "brave-falcon", URL: "https://x", Probed: true, Alive: false}, wantGlyph: "●  ", wantInVal: "brave-falcon", wantSuffix: "(unreachable)"},
		{name: "indeterminate with name → ●", env: preview.Environment{Name: "brave-falcon", URL: "https://x"}, err: &preview.ProbeError{URL: "https://x"}, wantGlyph: "●  ", wantInVal: "brave-falcon", wantSuffix: "(unverified)"},
		{name: "unprobable (no URL template) → ●", env: preview.Environment{Name: "brave-falcon"}, wantGlyph: "●  ", wantInVal: "brave-falcon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, v := statusPreviewRow(tc.env, tc.err)
			if tc.wantEmpty {
				if g != "" || v != "" {
					t.Errorf("expected empty row, got glyph=%q value=%q", g, v)
				}
				return
			}
			if stripped := stripANSI(g); !strings.Contains(stripped, tc.wantGlyph) {
				t.Errorf("glyph %q lacks %q", stripped, tc.wantGlyph)
			}
			if !strings.Contains(stripANSI(v), tc.wantInVal) {
				t.Errorf("value %q lacks %q", stripANSI(v), tc.wantInVal)
			}
			if tc.wantSuffix != "" && !strings.Contains(stripANSI(v), tc.wantSuffix) {
				t.Errorf("value %q lacks suffix %q", stripANSI(v), tc.wantSuffix)
			}
		})
	}
}

func TestStatusPreviewValue(t *testing.T) {
	cases := []struct {
		name string
		env  preview.Environment
		err  error
		want string // substring expected in the rendered value
	}{
		{name: "no env bound", err: preview.ErrNoEnvironment, want: "(none)"},
		{name: "other error", err: errors.New("boom"), want: "(unavailable)"},
		{name: "indeterminate with name", env: preview.Environment{Name: "brave-falcon"}, err: &preview.ProbeError{URL: "https://x"}, want: "(unverified)"},
		{name: "indeterminate empty name → none", err: &preview.ProbeError{URL: "https://x"}, want: "(none)"},
		{name: "alive", env: preview.Environment{Name: "brave-falcon", URL: "https://x", Probed: true, Alive: true}, want: "brave-falcon"},
		{name: "probed dead → unreachable", env: preview.Environment{Name: "brave-falcon", URL: "https://x", Probed: true, Alive: false}, want: "(unreachable)"},
		{name: "unprobable", env: preview.Environment{Name: "brave-falcon"}, want: "brave-falcon"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(statusPreviewValue(tc.env, tc.err))
			if !strings.Contains(got, tc.want) {
				t.Errorf("got %q, want substring %q", got, tc.want)
			}
		})
	}
}

func TestLifecycleKeyForStatus(t *testing.T) {
	// Reverse-lookup against schema defaults (no viper config set,
	// so resolveStatus falls back to defaults from schema.go).
	cases := []struct {
		name   string
		status string
		want   string
	}{
		{name: "Ready", status: "Ready", want: "ready"},
		{name: "In Progress", status: "In Progress", want: "in_progress"},
		{name: "Blocked", status: "Blocked", want: "blocked"},
		{name: "Review", status: "Review", want: "review"},
		{name: "Ready for Release", status: "Ready for Release", want: "ready_for_release"},
		{name: "Acceptance", status: "Acceptance", want: "acceptance"},
		{name: "Done", status: "Done", want: "done"},
		{name: "case-insensitive", status: "READY FOR RELEASE", want: "ready_for_release"},
		{name: "unmapped", status: "Frobnicated", want: ""},
		{name: "empty", status: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lifecycleKeyForStatus(tc.status); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLifecycleKeyGlyph(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		wantGlyph string
	}{
		// Only "done" is terminal (RoleDone / purple ✓). Every other
		// lifecycle stage is an active state and renders as ●.
		{name: "done → ✓", key: "done", wantGlyph: "✓  "},
		{name: "ready_for_release → ●", key: "ready_for_release", wantGlyph: "●  "},
		{name: "blocked → ●", key: "blocked", wantGlyph: "●  "},
		{name: "in_progress → ●", key: "in_progress", wantGlyph: "●  "},
		{name: "review → ●", key: "review", wantGlyph: "●  "},
		{name: "preview → ●", key: "preview", wantGlyph: "●  "},
		{name: "ready → ●", key: "ready", wantGlyph: "●  "},
		{name: "acceptance → ●", key: "acceptance", wantGlyph: "●  "},
		{name: "unknown key → ●", key: "garbage", wantGlyph: "●  "},
		{name: "empty key → ●", key: "", wantGlyph: "●  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, _ := lifecycleKeyGlyph(tc.key)
			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}
		})
	}
}

func TestStatusUpdatedGlyph(t *testing.T) {
	cases := []struct {
		name      string
		days      int
		wantGlyph string
	}{
		{name: "today (fresh)", days: 0, wantGlyph: "●  "},
		{name: "6 days (fresh)", days: 6, wantGlyph: "●  "},
		{name: "7 days (boundary → stale)", days: 7, wantGlyph: "●  "},
		{name: "29 days (stale)", days: 29, wantGlyph: "●  "},
		{name: "30 days (boundary → very stale)", days: 30, wantGlyph: "●  "},
		{name: "100 days (very stale)", days: 100, wantGlyph: "●  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, _ := statusUpdatedGlyph(tc.days)
			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}
		})
	}
}

func TestHumanizeAge(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "< 1 minute", d: 30 * time.Second, want: "just now"},
		{name: "minutes", d: 5 * time.Minute, want: "5m ago"},
		{name: "hours", d: 3 * time.Hour, want: "3h ago"},
		{name: "days", d: 2 * 24 * time.Hour, want: "2d ago"},
		{name: "months", d: 60 * 24 * time.Hour, want: "2mo ago"},
		{name: "years", d: 2 * 365 * 24 * time.Hour, want: "2y ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanizeAge(tc.d); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusUpdatedRow(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		t         time.Time
		wantEmpty bool
		wantGlyph string
	}{
		{name: "zero time → skip", t: time.Time{}, wantEmpty: true},
		{name: "fresh (1h ago) → ●", t: now.Add(-1 * time.Hour), wantGlyph: "●  "},
		{name: "stale (10d ago) → ●", t: now.Add(-10 * 24 * time.Hour), wantGlyph: "●  "},
		{name: "very stale (60d ago) → ●", t: now.Add(-60 * 24 * time.Hour), wantGlyph: "●  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, v := statusUpdatedRow(tc.t)
			if tc.wantEmpty {
				if g != "" || v != "" {
					t.Errorf("expected empty row, got %q / %q", g, v)
				}
				return
			}
			if !strings.Contains(stripANSI(g), tc.wantGlyph) {
				t.Errorf("glyph %q lacks %q", stripANSI(g), tc.wantGlyph)
			}
			if !strings.Contains(stripANSI(v), "ago") {
				t.Errorf("value %q missing humanized age", stripANSI(v))
			}
		})
	}
}

func TestStatusUpdatedValue(t *testing.T) {
	if got := stripANSI(statusUpdatedValue(time.Time{})); !strings.Contains(got, "(unknown)") {
		t.Errorf("zero time → got %q, want (unknown)", got)
	}
	if got := stripANSI(statusUpdatedValue(time.Now().Add(-2 * time.Hour))); !strings.Contains(got, "2h ago") {
		t.Errorf("got %q, want 2h ago", got)
	}
}

func TestProjectRepoColumns(t *testing.T) {
	repos := []projectRepoEntry{
		{name: "alpha"},
		{name: "beta"},
		{name: "gamma"},
		{name: "delta"},
		{name: "epsilon"},
		{name: "zeta"},
	}

	// Wide terminal, default minRows=4 → expect 2 cols × 3 rows
	// (column-major: col 0 = alpha/beta/gamma, col 1 = delta/epsilon/zeta).
	got := projectRepoColumns(repos, 80, 2, 4)
	if len(got) != 3 {
		t.Fatalf("expected 3 rows, got %d: %v", len(got), got)
	}
	// Column 0 entries should appear first on each row.
	if !strings.Contains(stripANSI(got[0]), "alpha") {
		t.Errorf("row 0 should start with alpha; got %q", got[0])
	}
	if !strings.Contains(stripANSI(got[0]), "delta") {
		t.Errorf("row 0 should also contain delta (col 1); got %q", got[0])
	}
	// Last row in column 0 should be gamma; column 1 should be zeta.
	if !strings.Contains(stripANSI(got[2]), "gamma") {
		t.Errorf("row 2 should contain gamma; got %q", got[2])
	}
	if !strings.Contains(stripANSI(got[2]), "zeta") {
		t.Errorf("row 2 should contain zeta; got %q", got[2])
	}

	// Single column with 4 repos and minRows=4 → expect single column.
	got = projectRepoColumns(repos[:4], 80, 2, 4)
	if len(got) != 4 {
		t.Errorf("4 repos with minRows=4 should be single column (4 rows), got %d rows", len(got))
	}

	// Empty input.
	if got := projectRepoColumns(nil, 80, 2, 4); got != nil {
		t.Errorf("empty input should return nil, got %v", got)
	}

	// Narrow terminal forces single column even with many repos.
	got = projectRepoColumns(repos, 10, 2, 4)
	if len(got) != len(repos) {
		t.Errorf("narrow terminal should single-column (%d rows), got %d", len(repos), len(got))
	}
}

func TestOSC8Link(t *testing.T) {
	got := osc8Link("https://example.com", "click me")
	wantPrefix := "\x1b]8;;https://example.com\x1b\\"
	wantSuffix := "\x1b]8;;\x1b\\"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("missing OSC 8 open: %q", got)
	}
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("missing OSC 8 close: %q", got)
	}
	if !strings.Contains(got, "click me") {
		t.Errorf("missing visible text: %q", got)
	}
}

// stripANSI removes ANSI escape sequences so test assertions can
// match the visible text. Handles SGR (CSI ... m) and OSC 8 (ESC ]
// 8 ; ... ESC \) since both appear in our rendered strings.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Skip ESC sequences: find the terminator.
		// CSI: ESC [ ... <letter>
		// OSC: ESC ] ... ESC \
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			// CSI — skip until first letter.
			j := i + 2
			for j < len(s) && !isAlpha(s[j]) {
				j++
			}
			i = j + 1
		case ']':
			// OSC — skip until ESC \.
			j := strings.Index(s[i:], "\x1b\\")
			if j < 0 {
				return b.String()
			}
			i += j + 2
		default:
			i += 2
		}
	}
	return b.String()
}

func isAlpha(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// The stepper is where the single-cell box-drawing invariant is
// load-bearing at a distance: the elbow is positioned by multiplying
// stepperSlotWidth, so if the connector's width ever drifts from the
// constant, the label silently stops lining up under its dot. Assert
// the two agree rather than trusting the comment on the constant.
func TestStepperSlotWidthMatchesConnector(t *testing.T) {
	// One dot plus the connector that follows it.
	if got := len([]rune(stepperConnector)) + 1; got != stepperSlotWidth {
		t.Errorf("dot + connector spans %d cells, but stepperSlotWidth is %d", got, stepperSlotWidth)
	}
}

func TestRenderLifecycleStepper(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		wantGlyph string // glyph expected at the active slot
		wantIdx   int
	}{
		{name: "ready is the first slot", key: "ready", wantGlyph: ui.Palette.Active, wantIdx: 0},
		{name: "in_progress", key: "in_progress", wantGlyph: ui.Palette.Active, wantIdx: 1},
		// Blocked is a real column in the sprint-board model, but the
		// work in it is negatively interrupted — so it alone renders
		// ✗ where every other active slot renders ●.
		{name: "blocked renders a cross", key: "blocked", wantGlyph: ui.Palette.Cross, wantIdx: 2},
		{name: "review", key: "review", wantGlyph: ui.Palette.Active, wantIdx: 3},
		{name: "done is the last slot", key: "done", wantGlyph: ui.Palette.Active, wantIdx: 6},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripANSI(renderLifecycleStepper(tc.key))
			track, elbow, found := strings.Cut(out, "\n")
			if !found {
				t.Fatalf("stepper = %q, want a track line and an elbow line", out)
			}

			// The track is one glyph per slot joined by connectors, so
			// splitting on the connector recovers the slots.
			slots := strings.Split(track, stepperConnector)
			if len(slots) != len(stepperSlotKeys) {
				t.Fatalf("track has %d slots, want %d (%q)", len(slots), len(stepperSlotKeys), track)
			}
			for i, got := range slots {
				want := ui.Palette.Inactive
				if i == tc.wantIdx {
					want = tc.wantGlyph
				}
				if got != want {
					t.Errorf("slot %d = %q, want %q", i, got, want)
				}
			}

			// The elbow's indent is what points the label at the
			// active slot.
			wantIndent := tc.wantIdx * stepperSlotWidth
			if gotIndent := len(elbow) - len(strings.TrimLeft(elbow, " ")); gotIndent != wantIndent {
				t.Errorf("elbow indent = %d, want %d (slot %d)", gotIndent, wantIndent, tc.wantIdx)
			}
			if !strings.HasPrefix(strings.TrimLeft(elbow, " "), stepperElbow) {
				t.Errorf("elbow = %q, want it to start with %q", elbow, stepperElbow)
			}
		})
	}
}

func TestRenderLifecycleStepperUnmapped(t *testing.T) {
	out := stripANSI(renderLifecycleStepperUnmapped("Triaging"))
	track, elbow, found := strings.Cut(out, "\n")
	if !found {
		t.Fatalf("stepper = %q, want a track line and an elbow line", out)
	}

	// No slot is filled — the row reads "we got something, we don't
	// know where it sits."
	slots := strings.Split(track, stepperConnector)
	if len(slots) != len(stepperSlotKeys) {
		t.Fatalf("track has %d slots, want %d", len(slots), len(stepperSlotKeys))
	}
	for i, got := range slots {
		if got != ui.Palette.Inactive {
			t.Errorf("slot %d = %q, want the inactive glyph %q", i, got, ui.Palette.Inactive)
		}
	}

	// The elbow points at slot 0 and carries the raw status text.
	if strings.HasPrefix(elbow, " ") {
		t.Errorf("elbow = %q, want no indent (points at slot 0)", elbow)
	}
	if !strings.Contains(elbow, "Triaging") {
		t.Errorf("elbow = %q, want it to carry the raw status text", elbow)
	}

	t.Run("empty status falls back to unknown", func(t *testing.T) {
		_, elbow, _ := strings.Cut(stripANSI(renderLifecycleStepperUnmapped("")), "\n")
		if !strings.Contains(elbow, "(unknown)") {
			t.Errorf("elbow = %q, want it to contain %q", elbow, "(unknown)")
		}
	})
}
