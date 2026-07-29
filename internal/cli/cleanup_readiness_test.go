package cli

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/vcs"
)

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
