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
		{name: "open + unknown → pending", branchState: "in sync", pr: code.PullRequest{State: "open", MergeableState: "unknown"}, want: ui.CardWaiting},
		{name: "open + empty mergeable → pending", branchState: "in sync", pr: code.PullRequest{State: "open"}, want: ui.CardWaiting},
		// Defaults.
		{name: "draft → pending", branchState: "in sync", pr: code.PullRequest{State: "draft"}, want: ui.CardWaiting},
		{name: "no PR → pending", branchState: "in sync", pr: code.PullRequest{}, want: ui.CardWaiting},
		{name: "no PR + unpushed → pending", branchState: "unpushed 3", pr: code.PullRequest{}, want: ui.CardWaiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRepoCardState(tc.branchState, tc.pr); got != tc.want {
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
		{name: "in sync", sync: vcs.BranchSync{HasRemote: true}, wantGlyph: "✓  "},
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

func TestStatusPRStatusLabel(t *testing.T) {
	cases := []struct {
		name        string
		state       string
		reviewState string
		want        string
	}{
		{name: "merged", state: "merged", want: "merged"},
		{name: "draft", state: "draft", want: "draft"},
		{name: "closed", state: "closed", want: "closed"},
		{name: "open + approved → approved", state: "open", reviewState: "approved", want: "approved"},
		{name: "open + changes_requested → changes requested", state: "open", reviewState: "changes_requested", want: "changes requested"},
		{name: "open + awaiting → open", state: "open", reviewState: "awaiting", want: "open"},
		{name: "open + no review → open", state: "open", want: "open"},
		{name: "no PR → empty", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusPRStatusLabel(tc.state, tc.reviewState); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusPRGlyph(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		mergeState string
		wantGlyph  string
	}{
		{name: "merged", state: "merged", wantGlyph: "✓  "},
		{name: "open + clean → ready", state: "open", mergeState: "clean", wantGlyph: "●  "},
		{name: "open + dirty → blocked", state: "open", mergeState: "dirty", wantGlyph: "▲  "},
		{name: "open + behind → blocked", state: "open", mergeState: "behind", wantGlyph: "▲  "},
		{name: "open + blocked → blocked", state: "open", mergeState: "blocked", wantGlyph: "▲  "},
		{name: "open + unstable → blocked", state: "open", mergeState: "unstable", wantGlyph: "▲  "},
		{name: "open + unknown → meta", state: "open", mergeState: "unknown", wantGlyph: "?  "},
		{name: "open + empty → pending", state: "open", wantGlyph: "⧗  "},
		{name: "draft → pending", state: "draft", wantGlyph: "⧗  "},
		{name: "closed → broken", state: "closed", wantGlyph: "✗  "},
		{name: "no PR → pending", wantGlyph: "⧗  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			glyph, _ := statusPRGlyph(tc.state, tc.mergeState)
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
		{name: "alive → ✓ + name", env: preview.Environment{Name: "brave-falcon", URL: "https://x", Probed: true, Alive: true}, wantGlyph: "✓  ", wantInVal: "brave-falcon"},
		{name: "indeterminate with name → ?", env: preview.Environment{Name: "brave-falcon", URL: "https://x"}, err: &preview.ProbeError{URL: "https://x"}, wantGlyph: "?  ", wantInVal: "brave-falcon", wantSuffix: "(unverified)"},
		{name: "unprobable (no URL template) → ◦", env: preview.Environment{Name: "brave-falcon"}, wantGlyph: "◦  ", wantInVal: "brave-falcon"},
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
		{name: "done → ✓", key: "done", wantGlyph: "✓  "},
		{name: "ready_for_release → ●", key: "ready_for_release", wantGlyph: "●  "},
		{name: "blocked → ▲", key: "blocked", wantGlyph: "▲  "},
		{name: "in_progress → ⧗", key: "in_progress", wantGlyph: "⧗  "},
		{name: "review → ⧗", key: "review", wantGlyph: "⧗  "},
		{name: "preview → ⧗", key: "preview", wantGlyph: "⧗  "},
		{name: "ready → ⧗", key: "ready", wantGlyph: "⧗  "},
		{name: "acceptance → ⧗", key: "acceptance", wantGlyph: "⧗  "},
		{name: "unknown key → ⧗", key: "garbage", wantGlyph: "⧗  "},
		{name: "empty key → ⧗", key: "", wantGlyph: "⧗  "},
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
		{name: "today (fresh)", days: 0, wantGlyph: "◦  "},
		{name: "6 days (fresh)", days: 6, wantGlyph: "◦  "},
		{name: "7 days (boundary → stale)", days: 7, wantGlyph: "▲  "},
		{name: "29 days (stale)", days: 29, wantGlyph: "▲  "},
		{name: "30 days (boundary → very stale)", days: 30, wantGlyph: "✗  "},
		{name: "100 days (very stale)", days: 100, wantGlyph: "✗  "},
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
		{name: "fresh (1h ago) → ◦", t: now.Add(-1 * time.Hour), wantGlyph: "◦  "},
		{name: "stale (10d ago) → ▲", t: now.Add(-10 * 24 * time.Hour), wantGlyph: "▲  "},
		{name: "very stale (60d ago) → ✗", t: now.Add(-60 * 24 * time.Hour), wantGlyph: "✗  "},
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
