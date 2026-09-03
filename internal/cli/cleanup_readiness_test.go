package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
)

// findRowContaining returns the single rendered line carrying want,
// failing if there is not exactly one. Isolating the row is what lets
// a glyph assertion distinguish the row's own glyph from the card's
// leading state glyph.
func findRowContaining(t *testing.T, lines []string, want string) string {
	t.Helper()
	var found []string
	for _, l := range lines {
		if strings.Contains(l, want) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d rows containing %q, want exactly 1: %q", len(found), want, lines)
	}
	return found[0]
}

// The readiness card is the only consumer of three severity glyphs,
// and severity→glyph is the mapping a reader scans first. Assert each
// tier renders its own shape, and that the worst finding drives the
// card's own state.
func TestBuildCleanupReadinessCardGlyphs(t *testing.T) {
	cases := []struct {
		name      string
		severity  findingSeverity
		wantGlyph string
		wantState string // the card's leading state glyph
	}{
		{name: "block", severity: findingBlock, wantGlyph: ui.Palette.Cross, wantState: ui.Palette.Cross},
		{name: "warn", severity: findingWarn, wantGlyph: ui.Palette.Attention, wantState: ui.Palette.Attention},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := buildCleanupReadinessCard(
				[]repoCleanup{{
					repo:     Repository{Name: "api"},
					branch:   "main",
					findings: []cleanupFinding{{severity: tc.severity, code: "x", message: "something"}},
				}},
				nil,
			)
			lines := strings.Split(strings.TrimRight(stripANSI(card.Render()), "\n"), "\n")

			// Assert on the row itself, not the card. Checking the
			// whole render is satisfied by the leading state glyph
			// alone, which would pass even if glyphFor mapped the
			// severities backwards.
			row := findRowContaining(t, lines, "api")
			if !strings.Contains(row, tc.wantGlyph) {
				t.Errorf("row = %q, want the %s glyph %q", row, tc.name, tc.wantGlyph)
			}
			for _, other := range []string{ui.Palette.Check, ui.Palette.Cross, ui.Palette.Attention} {
				if other == tc.wantGlyph {
					continue
				}
				if strings.Contains(row, other) {
					t.Errorf("row = %q, want only the %s glyph, but it also carries %q", row, tc.name, other)
				}
			}

			// The card's own state still aggregates to the worst
			// finding, which for a single finding is this severity.
			if !strings.HasPrefix(strings.TrimLeft(lines[0], " "), tc.wantState) {
				t.Errorf("card leads with %q, want the state glyph %q", lines[0], tc.wantState)
			}
		})
	}

	// A repo with no findings collapses to a single safe row, and the
	// card as a whole reads as success.
	t.Run("safe", func(t *testing.T) {
		card := buildCleanupReadinessCard(
			[]repoCleanup{{repo: Repository{Name: "api"}, branch: "main"}},
			nil,
		)
		out := stripANSI(card.Render())
		if !strings.Contains(out, ui.Palette.Check) {
			t.Errorf("card = %q, want the check glyph %q", out, ui.Palette.Check)
		}
		if strings.Contains(out, ui.Palette.Cross) || strings.Contains(out, ui.Palette.Attention) {
			t.Errorf("card = %q, want no block/warn glyphs for an all-safe set", out)
		}
	})

	// Workspace findings render alongside repo rows and are labeled
	// "workspace" rather than a repo name.
	t.Run("workspace finding", func(t *testing.T) {
		card := buildCleanupReadinessCard(
			[]repoCleanup{{repo: Repository{Name: "api"}, branch: "main"}},
			[]cleanupFinding{{severity: findingWarn, code: "stray", message: "stray files"}},
		)
		out := stripANSI(card.Render())
		if !strings.Contains(out, "workspace") {
			t.Errorf("card = %q, want a workspace row", out)
		}
		if !strings.Contains(out, ui.Palette.Attention) {
			t.Errorf("card = %q, want the warn glyph %q", out, ui.Palette.Attention)
		}
	})
}

// TestClassifyRepo locks every row of the safety matrix. Each case
// fabricates the probe state, calls classifyRepo, and asserts the
// expected codes (or absence thereof) come back. Severity ordering
// is checked separately in TestClassifyRepoSeverityOrder.
func TestClassifyRepo(t *testing.T) {
	merged := &code.PullRequest{Number: 7, State: "merged", HeadSHA: "abc"}
	open := &code.PullRequest{Number: 8, State: "open"}
	draft := &code.PullRequest{Number: 9, State: "draft"}
	closed := &code.PullRequest{Number: 10, State: "closed"}

	tests := []struct {
		name      string
		probe     repoCleanupProbe
		wantCodes []string // sorted worst-first; empty = no findings (SAFE)
	}{
		{
			name: "safe: merged + remote auto-deleted (happy path)",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 0},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "abc", // matches merged.HeadSHA
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: nil,
		},
		{
			name: "safe: clean, merged, remote still present",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 0},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "abc",
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: nil,
		},
		{
			name: "block: dirty worktree",
			probe: repoCleanupProbe{
				dirty:         true,
				dirtyKnown:    true,
				syncKnown:     true,
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"dirty"},
		},
		{
			name: "block: never pushed and not in base",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 3},
				syncKnown:     true,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"unmerged-work"},
		},
		{
			name: "block: pushed, no PR, not in base",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 2},
				syncKnown:     true,
				pr:            nil,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"unmerged-work"},
		},
		{
			name: "block: closed-not-merged PR and not in base",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 1},
				syncKnown:     true,
				pr:            closed,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"unmerged-work"},
		},
		{
			name: "block: post-merge commits — HEAD past merged PR head, remote still present",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 2},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "xyz",
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"post-merge-commits"},
		},
		{
			name: "block: post-merge commits — HEAD past merged PR head, remote auto-deleted",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 1},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "xyz",
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"post-merge-commits"},
		},
		{
			name: "block: post-merge commits — HEAD diverges with Ahead == 0 (reset/amend)",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 0},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "xyz",
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"post-merge-commits"},
		},
		{
			// Regression: squash-merge with auto-deleted remote and no
			// post-merge local work. IsMergedInto returns false because
			// the squash commit doesn't share history with the branch's
			// commits, and Ahead-vs-base is non-zero for the same
			// reason. The HEAD-vs-PR.HeadSHA check is the only signal
			// that's correct here — they match, so this is SAFE.
			name: "safe: squash-merge — auto-deleted remote, HEAD matches PR head",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 3},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "abc", // matches merged.HeadSHA
				isMerged:      false, // squash doesn't preserve history
				isMergedKnown: true,
			},
			wantCodes: nil,
		},
		{
			// Same shape but with the remote branch still present.
			name: "safe: squash-merge — remote present, HEAD matches PR head",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 0},
				syncKnown:     true,
				pr:            merged,
				headSHA:       "abc",
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: nil,
		},
		{
			name: "warn: open PR",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				pr:            open,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"open-pr"},
		},
		{
			name: "warn: draft PR is treated as open",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				pr:            draft,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"open-pr"},
		},
		{
			name: "warn: closed-not-merged but commits are in base",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				pr:            closed,
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"closed-pr"},
		},
		{
			// The failed merge probe surfaces alongside the host error —
			// they're independent signals (git-local vs code host).
			name: "warn: host unreachable",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				hostErr:       fakeErr("connection refused"),
				isMergedKnown: false,
			},
			wantCodes: []string{"host-unreachable", "unverified"},
		},
		{
			name: "block + warn: dirty plus open PR (worst-first ordering)",
			probe: repoCleanupProbe{
				dirty:         true,
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				pr:            open,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"dirty", "open-pr"},
		},
		{
			// Regression (was: silent SAFE reading): merge probe failed
			// on a pushed-and-ahead branch — the unpushed commits BLOCK
			// regardless, and the failed probe reads as unverified.
			name: "block: merge probe unknown, pushed branch ahead of remote",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 1},
				syncKnown:     true,
				pr:            nil,
				hostErr:       fakeErr("network"),
				isMergedKnown: false,
			},
			wantCodes: []string{"unpushed-commits", "host-unreachable", "unverified"},
		},
		{
			// Regression: merge probe failure (origin/HEAD unset) with no
			// PR and no host error must not read as SAFE.
			name: "warn: merge probe failed, everything else clean",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 0},
				syncKnown:     true,
				isMergedKnown: false,
			},
			wantCodes: []string{"unverified"},
		},
		{
			// Fail closed: a never-pushed branch may hold the only copy
			// of its commits, so an inconclusive merge probe BLOCKs.
			name: "block: never pushed and merge probe failed",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: false},
				syncKnown:     true,
				isMergedKnown: false,
			},
			wantCodes: []string{"unverified-work"},
		},
		{
			// Regression (was: WARN-only): an open PR doesn't preserve
			// commits that were never pushed to its branch.
			name: "block: open PR with unpushed local commits",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true, Ahead: 2},
				syncKnown:     true,
				pr:            open,
				isMerged:      false,
				isMergedKnown: true,
			},
			wantCodes: []string{"unpushed-commits", "open-pr"},
		},
		{
			// A merged PR with the working tree and HEAD verified is
			// safe even when the sync probe failed — HeadSHA equality
			// already proves the local commits are captured.
			name: "safe: merged PR, sync probe failed but HEAD matches",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				pr:            merged,
				headSHA:       "abc",
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: nil,
		},
		{
			// Merged PR but the HEAD probe failed: post-merge divergence
			// can't be checked, so the gap surfaces instead of reading
			// as SAFE.
			name: "warn: merged PR but HEAD unknown",
			probe: repoCleanupProbe{
				dirtyKnown:    true,
				branchSync:    vcs.BranchSync{HasRemote: true},
				syncKnown:     true,
				pr:            merged,
				isMerged:      true,
				isMergedKnown: true,
			},
			wantCodes: []string{"unverified"},
		},
		{
			// The zero-value probe (every probe failed) fails closed as
			// unverified rather than silently SAFE.
			name:      "warn: zero-value probe is unverified, not safe",
			probe:     repoCleanupProbe{},
			wantCodes: []string{"unverified"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRepo(tt.probe)
			gotCodes := make([]string, len(got))
			for i, f := range got {
				gotCodes[i] = f.code
			}
			if !equalStringSlices(gotCodes, tt.wantCodes) {
				t.Errorf("codes = %v, want %v\nfindings = %+v", gotCodes, tt.wantCodes, got)
			}
		})
	}
}

// TestClassifyRepoSeverityOrder confirms findings come back in
// worst-first severity order even when multiple findings emit.
func TestClassifyRepoSeverityOrder(t *testing.T) {
	open := &code.PullRequest{Number: 1, State: "open"}
	probe := repoCleanupProbe{
		dirty:         true, // BLOCK
		branchSync:    vcs.BranchSync{HasRemote: true},
		pr:            open, // WARN
		isMerged:      false,
		isMergedKnown: true,
	}
	got := classifyRepo(probe)
	if len(got) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(got))
	}
	if got[0].severity != findingBlock {
		t.Errorf("first finding severity = %d, want BLOCK (%d)", got[0].severity, findingBlock)
	}
	if got[1].severity != findingWarn {
		t.Errorf("second finding severity = %d, want WARN (%d)", got[1].severity, findingWarn)
	}
}

func TestClassifyWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		probe     workspaceCleanupProbe
		wantCodes []string
	}{
		{
			name:      "all clean",
			probe:     workspaceCleanupProbe{issueStatus: "Done", issueDoneLike: true},
			wantCodes: nil,
		},
		{
			name:      "stray files block",
			probe:     workspaceCleanupProbe{strayFiles: []string{"scratch.md", "notes.txt"}},
			wantCodes: []string{"stray-files"},
		},
		{
			name:      "issue not done warns",
			probe:     workspaceCleanupProbe{issueStatus: "In Progress", issueDoneLike: false},
			wantCodes: []string{"issue-not-done"},
		},
		{
			name: "both: stray BLOCKs and issue WARNs, BLOCK first",
			probe: workspaceCleanupProbe{
				strayFiles:    []string{"x"},
				issueStatus:   "In Review",
				issueDoneLike: false,
			},
			wantCodes: []string{"stray-files", "issue-not-done"},
		},
		{
			name:      "missing issue status emits no warning",
			probe:     workspaceCleanupProbe{},
			wantCodes: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyWorkspace(tt.probe)
			gotCodes := make([]string, len(got))
			for i, f := range got {
				gotCodes[i] = f.code
			}
			if !equalStringSlices(gotCodes, tt.wantCodes) {
				t.Errorf("codes = %v, want %v\nfindings = %+v", gotCodes, tt.wantCodes, got)
			}
		})
	}
}

func TestStrayFilesMessage(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		contains string
	}{
		{"single file", []string{"a"}, "1 untracked file"},
		{"two files inline", []string{"a", "b"}, "a, b"},
		{"three files inline", []string{"a", "b", "c"}, "a, b, c"},
		{"more than three truncates", []string{"a", "b", "c", "d", "e"}, "and 2 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strayFilesMessage(tt.files)
			if !strings.Contains(got, tt.contains) {
				t.Errorf("strayFilesMessage(%v) = %q, want to contain %q", tt.files, got, tt.contains)
			}
		})
	}
}

func TestClassifyAllAggregatesWorst(t *testing.T) {
	// One repo BLOCKed, one clean, workspace WARN → worst is BLOCK.
	probes := []repoCleanupProbe{
		{dirty: true, dirtyKnown: true, repo: Repository{Name: "a"}},
		{
			repo:          Repository{Name: "b"},
			dirtyKnown:    true,
			branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 0},
			syncKnown:     true,
			pr:            &code.PullRequest{Number: 1, State: "merged", HeadSHA: "abc"},
			headSHA:       "abc",
			isMerged:      true,
			isMergedKnown: true,
		},
	}
	ws := workspaceCleanupProbe{issueStatus: "In Progress", issueDoneLike: false}
	_, _, worst := classifyAll(probes, ws)
	if worst != findingBlock {
		t.Errorf("worst = %d, want BLOCK (%d)", worst, findingBlock)
	}

	// All clean → SAFE.
	probes = []repoCleanupProbe{
		{
			repo:          Repository{Name: "a"},
			dirtyKnown:    true,
			branchSync:    vcs.BranchSync{HasRemote: false, Ahead: 0},
			syncKnown:     true,
			pr:            &code.PullRequest{Number: 1, State: "merged", HeadSHA: "abc"},
			headSHA:       "abc",
			isMerged:      true,
			isMergedKnown: true,
		},
	}
	ws = workspaceCleanupProbe{issueStatus: "Done", issueDoneLike: true}
	_, _, worst = classifyAll(probes, ws)
	if worst != findingSafe {
		t.Errorf("worst = %d, want SAFE (%d)", worst, findingSafe)
	}
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

func fakeErr(s string) error { return stringErr(s) }

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWorstFindingMessage pins the bulk row summary: the most severe
// finding wins (repo findings carry their repo name), and additional
// findings are counted rather than listed.
func TestWorstFindingMessage(t *testing.T) {
	t.Run("no findings summarizes to nothing", func(t *testing.T) {
		c := bulkCleanupCandidate{}
		if got := c.worstFindingMessage(); got != "" {
			t.Errorf("message = %q, want empty", got)
		}
	})

	t.Run("workspace finding renders bare", func(t *testing.T) {
		c := bulkCleanupCandidate{
			wsFindings: []cleanupFinding{{severity: findingBlock, code: "stray-files", message: "2 untracked file(s)"}},
		}
		if got := c.worstFindingMessage(); got != "2 untracked file(s)" {
			t.Errorf("message = %q, want the bare workspace finding", got)
		}
	})

	t.Run("repo finding carries the repo name", func(t *testing.T) {
		c := bulkCleanupCandidate{
			repoResults: []repoCleanup{{
				repo:     Repository{Name: "api"},
				findings: []cleanupFinding{{severity: findingBlock, code: "dirty", message: "uncommitted changes in worktree"}},
			}},
		}
		if got := c.worstFindingMessage(); got != "api: uncommitted changes in worktree" {
			t.Errorf("message = %q, want the repo-prefixed finding", got)
		}
	})

	t.Run("worst severity wins and the rest are counted", func(t *testing.T) {
		c := bulkCleanupCandidate{
			wsFindings: []cleanupFinding{{severity: findingWarn, code: "issue-not-done", message: "issue is In Progress"}},
			repoResults: []repoCleanup{{
				repo: Repository{Name: "api"},
				findings: []cleanupFinding{
					{severity: findingBlock, code: "dirty", message: "uncommitted changes in worktree"},
					{severity: findingWarn, code: "open-pr", message: "PR #4 is open"},
				},
			}},
		}
		got := c.worstFindingMessage()
		if !strings.HasPrefix(got, "api: uncommitted changes in worktree") {
			t.Errorf("message = %q, want it led by the BLOCK finding", got)
		}
		if !strings.Contains(got, "(+2 more)") {
			t.Errorf("message = %q, want the remaining findings counted", got)
		}
	})
}

// TestBuildBulkCleanupReadinessCard covers the raw-mode bulk card:
// one row per workspace with the severity glyph mapping, exclusion
// annotated when --force is absent, and worst-first ordering.
func TestBuildBulkCleanupReadinessCard(t *testing.T) {
	safe := bulkCleanupCandidate{target: cleanupTarget{workspace: "EX-1-safe"}}
	warned := bulkCleanupCandidate{
		target:     cleanupTarget{workspace: "EX-2-warned"},
		wsFindings: []cleanupFinding{{severity: findingWarn, code: "issue-not-done", message: "issue is In Progress"}},
		worst:      findingWarn,
	}
	blocked := bulkCleanupCandidate{
		target: cleanupTarget{workspace: "EX-3-blocked"},
		repoResults: []repoCleanup{{
			repo:     Repository{Name: "api"},
			findings: []cleanupFinding{{severity: findingBlock, code: "dirty", message: "uncommitted changes in worktree"}},
		}},
		worst: findingBlock,
	}

	t.Run("severity glyphs and exclusion annotation", func(t *testing.T) {
		card := buildBulkCleanupReadinessCard([]bulkCleanupCandidate{safe, warned, blocked}, false)
		lines := strings.Split(strings.TrimRight(stripANSI(card.Render()), "\n"), "\n")

		row := findRowContaining(t, lines, "EX-1-safe")
		if !strings.Contains(row, ui.Palette.Check) {
			t.Errorf("safe row = %q, want the check glyph", row)
		}
		row = findRowContaining(t, lines, "EX-2-warned")
		if !strings.Contains(row, ui.Palette.Attention) || !strings.Contains(row, "issue is In Progress") {
			t.Errorf("warn row = %q, want the warn glyph and message", row)
		}
		row = findRowContaining(t, lines, "EX-3-blocked")
		if !strings.Contains(row, ui.Palette.Cross) || !strings.Contains(row, "excluded") {
			t.Errorf("block row = %q, want the block glyph and the exclusion annotation", row)
		}

		// Worst-first: the blocked workspace's row renders above the
		// safe one's.
		var blockedIdx, safeIdx int
		for i, l := range lines {
			if strings.Contains(l, "EX-3-blocked") {
				blockedIdx = i
			}
			if strings.Contains(l, "EX-1-safe") {
				safeIdx = i
			}
		}
		if blockedIdx > safeIdx {
			t.Errorf("blocked row at %d renders below safe row at %d, want worst-first", blockedIdx, safeIdx)
		}
	})

	t.Run("force drops the exclusion annotation", func(t *testing.T) {
		card := buildBulkCleanupReadinessCard([]bulkCleanupCandidate{blocked}, true)
		out := stripANSI(card.Render())
		if strings.Contains(out, "excluded") {
			t.Errorf("card = %q, says excluded but --force includes it", out)
		}
	})
}

// TestEmitBulkCleanupReadinessNonInteractive drives the raw-mode bulk
// readiness path directly. Plain unit tests run with go test's
// non-TTY stdin, so isInteractive() is false here — the same posture
// a piped/CI bosun run has, which the interactive e2e harness (whose
// injected readers always read as interactive) structurally can't
// exercise.
//
// The candidate states are built from real probes against fabricated
// disk state: a stray file in the workspace dir is a BLOCK, an empty
// dir is SAFE, and a repo path that isn't a git repository yields the
// probe-integrity WARN (nothing could be verified).
func TestEmitBulkCleanupReadinessNonInteractive(t *testing.T) {
	if isInteractive() {
		t.Fatal("test requires go test's non-TTY stdin; the raw-mode branch is the subject")
	}
	ctx := context.Background()
	g := git.New()

	blockedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(blockedDir, "notes.md"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	blocked := cleanupTarget{workspace: "EX-1-blocked", wsPath: blockedDir}
	safe := cleanupTarget{workspace: "EX-2-safe", wsPath: t.TempDir()}
	warned := cleanupTarget{
		workspace: "EX-3-warned",
		wsPath:    t.TempDir(),
		repos:     []Repository{{Name: "api", Path: t.TempDir()}}, // not a git repo → unverified WARN
	}

	t.Run("warnings error without force", func(t *testing.T) {
		_, err := emitBulkCleanupReadiness(ctx, g, nil, nil, []cleanupTarget{blocked, safe, warned}, false)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Errorf("err = %v, want the warnings-need---force refusal", err)
		}
	})

	t.Run("blocked is excluded and the clean rest proceed", func(t *testing.T) {
		included, err := emitBulkCleanupReadiness(ctx, g, nil, nil, []cleanupTarget{blocked, safe}, false)
		if err != nil {
			t.Fatalf("err = %v, want nil (exclusion is the sweep's posture, not an error)", err)
		}
		if len(included) != 1 || included[0].target.workspace != "EX-2-safe" {
			t.Errorf("included = %+v, want just the safe workspace", included)
		}
	})

	t.Run("force includes blocks and acknowledges warns", func(t *testing.T) {
		included, err := emitBulkCleanupReadiness(ctx, g, nil, nil, []cleanupTarget{blocked, warned}, true)
		if err != nil {
			t.Fatalf("err = %v, want nil (--force is the non-interactive acknowledgement)", err)
		}
		if len(included) != 2 {
			t.Errorf("included = %d candidates, want both", len(included))
		}
	})
}

// TestIncludeBulkCandidates pins the sweep's exclusion rule.
func TestIncludeBulkCandidates(t *testing.T) {
	candidates := []bulkCleanupCandidate{
		{target: cleanupTarget{workspace: "safe"}},
		{target: cleanupTarget{workspace: "warned"}, worst: findingWarn},
		{target: cleanupTarget{workspace: "blocked"}, worst: findingBlock},
	}

	names := func(in []bulkCleanupCandidate) []string {
		var out []string
		for _, c := range in {
			out = append(out, c.target.workspace)
		}
		return out
	}

	if got := names(includeBulkCandidates(candidates, false)); !equalStringSlices(got, []string{"safe", "warned"}) {
		t.Errorf("without force = %v, want blocked excluded", got)
	}
	if got := names(includeBulkCandidates(candidates, true)); !equalStringSlices(got, []string{"safe", "warned", "blocked"}) {
		t.Errorf("with force = %v, want everything included", got)
	}
}
