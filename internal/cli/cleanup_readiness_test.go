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

// TestBulkPickerLabel pins the picker rows' contract: bold name and
// dimmed BRIEF reason via raw SGR intensity toggles; no --force hint
// (the validation message teaches the gate); a terse "(+N)" tally;
// and — the part that breaks huh if violated — no lipgloss-style
// full SGR reset (\x1b[0m or bare \x1b[m), which would wipe huh's
// own selection/focus styling for the rest of the line.
func TestBulkPickerLabel(t *testing.T) {
	warned := bulkCleanupCandidate{
		target: cleanupTarget{workspace: "EX-2-warned"},
		wsFindings: []cleanupFinding{{
			severity: findingWarn, code: "issue-not-done",
			message: "issue is Ready for Release, not in a done-like status",
			brief:   "Ready for Release",
		}},
		worst: findingWarn,
	}
	blocked := bulkCleanupCandidate{
		target: cleanupTarget{workspace: "EX-3-blocked"},
		repoResults: []repoCleanup{{
			repo: Repository{Name: "api"},
			findings: []cleanupFinding{
				{severity: findingBlock, code: "dirty", message: "uncommitted changes in worktree", brief: "uncommitted changes"},
				{severity: findingWarn, code: "open-pr", message: "PR #8 is open", brief: "PR #8 open"},
			},
		}},
		worst: findingBlock,
	}

	safe := bulkPickerLabel(bulkCleanupCandidate{target: cleanupTarget{workspace: "EX-1-safe"}})
	if safe != "\x1b[1mEX-1-safe\x1b[22m" {
		t.Errorf("safe label = %q, want the bare bolded name", safe)
	}

	warn := bulkPickerLabel(warned)
	if !strings.Contains(warn, "\x1b[2m") || !strings.Contains(warn, "Ready for Release") {
		t.Errorf("warn label = %q, want the dimmed brief reason", warn)
	}
	if strings.Contains(warn, "done-like status") {
		t.Errorf("warn label = %q, the verbose message leaked into the picker", warn)
	}

	block := bulkPickerLabel(blocked)
	if !strings.Contains(block, "api: uncommitted changes") || !strings.Contains(block, "(+1)") {
		t.Errorf("block label = %q, want the brief lead finding and the terse tally", block)
	}
	if strings.Contains(block, "--force") {
		t.Errorf("block label = %q, the --force hint must not appear (validation teaches the gate)", block)
	}
	if strings.Contains(block, "in worktree") {
		t.Errorf("block label = %q, the verbose message leaked into the picker", block)
	}

	// A finding without a brief falls back to its message.
	fallback := bulkPickerLabel(bulkCleanupCandidate{
		target:     cleanupTarget{workspace: "EX-4"},
		wsFindings: []cleanupFinding{{severity: findingWarn, code: "x", message: "only message"}},
		worst:      findingWarn,
	})
	if !strings.Contains(fallback, "only message") {
		t.Errorf("fallback label = %q, want the message when brief is empty", fallback)
	}

	for _, label := range []string{safe, warn, block} {
		if strings.Contains(label, "\x1b[0m") || strings.Contains(label, "\x1b[m") {
			t.Errorf("label %q carries a full SGR reset, which wipes huh's line styling", label)
		}
	}
}

// TestBuildBulkSelectionCard covers the post-picker record — the one
// card that replaces both the readiness card and the submitted
// multi-select: readiness rows in picker order with the selection
// folded in, unselected rows fully receded, the picker's --force
// hint dropped, and the readiness card's worst-first state kept.
func TestBuildBulkSelectionCard(t *testing.T) {
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
	candidates := []bulkCleanupCandidate{safe, warned, blocked}

	// Safe and warned selected; blocked left unselected.
	card := buildBulkSelectionCard(candidates, map[int]bool{0: true, 1: true})
	out := stripANSI(card.Render())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	if !strings.Contains(out, "2 of 3 selected") {
		t.Errorf("card = %q, want the selection tally in the title", out)
	}

	row := findRowContaining(t, lines, "EX-1-safe")
	if !strings.Contains(row, ui.Palette.Check) {
		t.Errorf("selected safe row = %q, want the check glyph", row)
	}
	row = findRowContaining(t, lines, "EX-2-warned")
	if !strings.Contains(row, ui.Palette.Attention) || !strings.Contains(row, "issue is In Progress") {
		t.Errorf("selected warn row = %q, want the warn glyph and reason", row)
	}
	row = findRowContaining(t, lines, "EX-3-blocked")
	if !strings.Contains(row, ui.Palette.Inactive) {
		t.Errorf("unselected row = %q, want the receded %q glyph", row, ui.Palette.Inactive)
	}
	if !strings.Contains(row, "uncommitted changes") {
		t.Errorf("unselected row = %q, want its reason kept", row)
	}
	if strings.Contains(out, "--force to select") {
		t.Errorf("card = %q, the picker's gating hint must not survive into the record", out)
	}

	// Rows stay in picker (candidate) order — the in-place swap
	// depends on it — unlike the raw readiness card's worst-first.
	safeIdx := -1
	blockedIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "EX-1-safe") {
			safeIdx = i
		}
		if strings.Contains(l, "EX-3-blocked") {
			blockedIdx = i
		}
	}
	if safeIdx > blockedIdx {
		t.Errorf("rows reordered (safe %d, blocked %d), want candidate order", safeIdx, blockedIdx)
	}

	// A selected --force-included block keeps its error glyph; the
	// aggregate state stays worst-first like the readiness card.
	forced := buildBulkSelectionCard(candidates, map[int]bool{2: true})
	forcedOut := stripANSI(forced.Render())
	row = findRowContaining(t, strings.Split(forcedOut, "\n"), "EX-3-blocked")
	if !strings.Contains(row, ui.Palette.Cross) {
		t.Errorf("force-selected block row = %q, want the block glyph", row)
	}
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

	t.Run("returns every candidate classified", func(t *testing.T) {
		// The gate moved to the caller (#120): the readiness pass
		// renders and classifies but excludes nothing itself —
		// includeBulkCandidates (non-interactive) or the picker
		// (interactive) decide who proceeds.
		candidates, rewind, err := emitBulkCleanupReadiness(ctx, g, nil, nil, []cleanupTarget{blocked, safe, warned}, false)
		if err != nil {
			t.Fatalf("err = %v, want nil (classification is not a gate)", err)
		}
		if rewind != nil {
			t.Error("non-interactive readiness returned a rewind; the raw card is the durable record and must stand")
		}
		if len(candidates) != 3 {
			t.Fatalf("candidates = %d, want all 3", len(candidates))
		}
		wants := []findingSeverity{findingBlock, findingSafe, findingWarn}
		for i, want := range wants {
			if candidates[i].worst != want {
				t.Errorf("candidate %d (%s) worst = %v, want %v",
					i, candidates[i].target.workspace, candidates[i].worst, want)
			}
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
