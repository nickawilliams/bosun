package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/fsutil"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
)

// cleanupReadinessProbeTimeout caps the duration of optional probes
// (issue tracker) so a flaky source doesn't dominate the gather phase.
const cleanupReadinessProbeTimeout = 3 * time.Second

// findingSeverity ranks safety findings worst-first (highest int = most
// severe). Used to aggregate per-repo and per-workspace findings into a
// single card glyph and to drive the gate decision.
type findingSeverity int

const (
	findingSafe findingSeverity = iota
	findingWarn
	findingBlock
)

// cleanupFinding describes a single signal that bears on whether
// cleanup is safe to run. code is a stable identifier suitable for
// tests and grep; message is the user-facing prose rendered in the
// card row.
type cleanupFinding struct {
	severity findingSeverity
	code     string
	message  string
}

// repoCleanupProbe is the raw signal set classify() consumes for one
// repo. Separated from the gather flow so the classification logic can
// be tested in isolation against fabricated inputs without needing to
// mock git / host / tracker.
type repoCleanupProbe struct {
	repo   Repository // worktree path on .Path
	branch string

	dirty         bool
	dirtyKnown    bool // false when the IsDirty probe failed
	headSHA       string
	branchSync    vcs.BranchSync
	syncKnown     bool // false when the GetBranchSync probe failed
	isMerged      bool // branch is ancestor of base
	isMergedKnown bool // false when IsMergedInto wasn't or couldn't be run

	pr      *code.PullRequest // nil when no PR record (or host unreachable)
	hostErr error             // non-nil when PR lookup itself failed
}

// workspaceCleanupProbe holds the workspace-scoped signal set —
// signals that aren't per-repo (stray files in the workspace dir,
// issue tracker status). Classified separately by classifyWorkspace.
type workspaceCleanupProbe struct {
	strayFiles    []string // file/dir names in wsPath that aren't repo subdirs
	issueStatus   string   // empty when the probe was skipped (timeout, no tracker)
	issueDoneLike bool     // true when issueStatus maps to the "done" lifecycle key
}

// repoCleanup pairs a repo with its classified findings. Sorted
// worst-first so the card's row order matches the gate's severity
// reading.
type repoCleanup struct {
	repo     Repository
	branch   string
	findings []cleanupFinding
}

// classifyRepo turns a probe result into the set of findings the
// safety matrix describes. Pure — no I/O, no shared state — so the
// matrix can be locked in by table tests.
func classifyRepo(p repoCleanupProbe) []cleanupFinding {
	var out []cleanupFinding

	if p.dirty {
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "dirty",
			message:  "uncommitted changes in worktree",
		})
	}

	prMerged := p.pr != nil && p.pr.State == "merged"
	prOpen := p.pr != nil && (p.pr.State == "open" || p.pr.State == "draft")
	prClosedNoMerge := p.pr != nil && p.pr.State == "closed"

	// Data-preservation BLOCKs — emit at most one "unmerged-work"-
	// shaped finding describing the dominant reason. The branches all
	// describe the same conceptual danger ("work exists that isn't
	// in base and isn't represented by a merged PR"); the message
	// varies so the user knows which probe fired. The unverified-work
	// case fails closed: a never-pushed branch may hold the only copy
	// of its commits, so an inconclusive merge probe can't clear it.
	preBlocks := len(out)
	switch {
	case prMerged:
		// PR merged — data is preserved by GitHub's PR history. No
		// unmerged-work finding; post-merge-commits below catches
		// the case where the user kept committing past the merge.
	case !p.branchSync.HasRemote && p.isMergedKnown && !p.isMerged:
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "unmerged-work",
			message:  "branch was never pushed and commits aren't in base",
		})
	case p.syncKnown && !p.branchSync.HasRemote && !p.isMergedKnown:
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "unverified-work",
			message:  "branch was never pushed and merge status couldn't be verified",
		})
	case p.branchSync.HasRemote && p.pr == nil && p.isMergedKnown && !p.isMerged:
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "unmerged-work",
			message:  "branch is pushed but has no PR and isn't in base",
		})
	case prClosedNoMerge && p.isMergedKnown && !p.isMerged:
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "unmerged-work",
			message:  fmt.Sprintf("PR #%d was closed without merge and commits aren't in base", p.pr.Number),
		})
	}
	unmergedFired := len(out) > preBlocks

	// Unpushed local commits — the remote branch exists but local
	// HEAD is ahead of it, so those commits exist nowhere else.
	// Skipped when an unmerged-work finding already fired (the
	// dominant reason wins) and for merged PRs, where the HeadSHA
	// comparison below is the merge-method-correct signal.
	if !prMerged && !unmergedFired && p.syncKnown &&
		p.branchSync.HasRemote && p.branchSync.Ahead > 0 {
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "unpushed-commits",
			message:  fmt.Sprintf("%d local commit(s) aren't on the remote branch", p.branchSync.Ahead),
		})
	}

	// Post-merge work — local HEAD diverges from what GitHub merged.
	// Use the HEAD vs PR.HeadSHA comparison as the canonical signal
	// rather than `commits ahead of base`: squash and rebase merges
	// produce a base that doesn't share history with the branch's
	// pre-merge commits, so Ahead-vs-base would mark every squash-
	// merged branch with auto-deleted remote as "post-merge work"
	// even when the user did nothing locally. The HeadSHA equality
	// check is the only signal that's correct across merge methods —
	// if the local HEAD is the exact commit GitHub merged, the work
	// is fully captured by the PR no matter how the integration
	// happened; if HEAD has moved past it, those new commits aren't.
	if prMerged && p.pr.HeadSHA != "" && p.headSHA != "" && p.pr.HeadSHA != p.headSHA {
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "post-merge-commits",
			message:  "local HEAD has commits past the merged PR; deleting the branch would lose them",
		})
	}

	// WARN findings — status mismatches that don't lose data.
	if prOpen {
		out = append(out, cleanupFinding{
			severity: findingWarn,
			code:     "open-pr",
			message:  fmt.Sprintf("PR #%d is %s", p.pr.Number, p.pr.State),
		})
	}
	if prClosedNoMerge && p.isMergedKnown && p.isMerged {
		// Closed without merge but commits ARE in base — work is
		// preserved (via some other mechanism: rebase, fast-forward,
		// another PR). Surface as WARN so the user notices the
		// closed-without-merge oddity.
		out = append(out, cleanupFinding{
			severity: findingWarn,
			code:     "closed-pr",
			message:  fmt.Sprintf("PR #%d was closed without merge (commits are in base)", p.pr.Number),
		})
	}
	if p.hostErr != nil {
		out = append(out, cleanupFinding{
			severity: findingWarn,
			code:     "host-unreachable",
			message:  "couldn't reach the code host to check PR state",
		})
	}

	// Probe-integrity WARN — a failed probe means a safety signal
	// above silently didn't run, so "no findings" must not read as
	// SAFE. Named per missing signal so the user knows what wasn't
	// checked. prMerged narrows the set: once GitHub holds the merged
	// PR, only working-tree state and post-merge divergence still
	// bear on data loss.
	var unverified []string
	if !p.dirtyKnown {
		unverified = append(unverified, "working tree status")
	}
	switch {
	case !prMerged:
		if !p.syncKnown {
			unverified = append(unverified, "branch sync")
		}
		if !p.isMergedKnown && !unmergedFired {
			unverified = append(unverified, "merge ancestry")
		}
	case p.headSHA == "" || p.pr.HeadSHA == "":
		unverified = append(unverified, "post-merge divergence")
	}
	if len(unverified) > 0 {
		out = append(out, cleanupFinding{
			severity: findingWarn,
			code:     "unverified",
			message:  fmt.Sprintf("couldn't verify %s; findings may be incomplete", strings.Join(unverified, ", ")),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].severity > out[j].severity
	})
	return out
}

// classifyWorkspace turns workspace-scoped probe results into
// workspace-level findings. Stray files BLOCK because cleanup's
// post-Apply RemoveAll destroys them; an unfinished issue WARNs
// because the lifecycle is the user's concern, not cleanup's.
func classifyWorkspace(p workspaceCleanupProbe) []cleanupFinding {
	var out []cleanupFinding
	if len(p.strayFiles) > 0 {
		out = append(out, cleanupFinding{
			severity: findingBlock,
			code:     "stray-files",
			message:  strayFilesMessage(p.strayFiles),
		})
	}
	if p.issueStatus != "" && !p.issueDoneLike {
		out = append(out, cleanupFinding{
			severity: findingWarn,
			code:     "issue-not-done",
			message:  fmt.Sprintf("issue is %s, not in a done-like status", p.issueStatus),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].severity > out[j].severity
	})
	return out
}

func strayFilesMessage(files []string) string {
	const limit = 3
	if len(files) <= limit {
		return fmt.Sprintf("%d untracked file(s) in workspace dir: %s", len(files), strings.Join(files, ", "))
	}
	return fmt.Sprintf("%d untracked file(s) in workspace dir: %s, and %d more",
		len(files), strings.Join(files[:limit], ", "), len(files)-limit)
}


// emitCleanupReadiness gathers each repo's safety signals + workspace-
// scoped signals under one spinner, classifies them via the safety
// matrix, renders the result card, and gates accordingly.
//
//   - Any BLOCK without --force → returns ErrCancelled with the card on
//     screen showing the blockers. (Caller's responsibility to detect
//     `errors.Is(err, ErrCancelled)` and exit cleanly.)
//   - Any WARN (or --force overriding BLOCKs) → Continue/Cancel prompt
//     focused on Cancel; Continue returns nil, Cancel returns ErrCancelled.
//   - All SAFE → static card prints, no prompt, returns nil.
//
// In raw / non-interactive mode the card prints and BLOCK findings
// still produce ErrCancelled (cleanup can't be silently destructive);
// --force forces success the way it does interactively.
func emitCleanupReadiness(
	ctx context.Context,
	g vcs.VCS,
	host code.Host,
	tracker issue.Tracker,
	repos []Repository,
	wsPath string,
	issueKey string,
	force bool,
) ([]repoCleanup, []cleanupFinding, error) {
	probes := make([]repoCleanupProbe, len(repos))
	var workspaceProbe workspaceCleanupProbe

	// Raw / non-interactive mode: no group, no spinners. Gather
	// sequentially, classify, print the static summary.
	if !isInteractive() {
		for i, r := range repos {
			probes[i] = gatherRepoProbe(ctx, g, host, r)
		}
		workspaceProbe = gatherWorkspaceProbe(ctx, tracker, repos, wsPath, issueKey)
		repoResults, wsFindings, worstSeverity := classifyAll(probes, workspaceProbe)
		buildCleanupReadinessCard(repoResults, wsFindings).Print()
		if worstSeverity == findingBlock && !force {
			return repoResults, wsFindings, fmt.Errorf("cleanup readiness: blocking findings present; re-run with --force to override")
		}
		return repoResults, wsFindings, nil
	}

	// Interactive: one outer card with the workspace probe first
	// (rendered with a non-keyword title color so it reads visually
	// distinct from the identifier-shaped repo rows that follow),
	// then a child spinner per repo that resolves to its result
	// row(s) when the probe finishes.
	ui.RunGroup("cleanup readiness", func(grp ui.Reporter) {
		_ = grp.Spinner("workspace", func() error {
			workspaceProbe = gatherWorkspaceProbe(ctx, tracker, repos, wsPath, issueKey)
			return nil
		})
		emitWorkspaceRows(grp, classifyWorkspace(workspaceProbe))

		for i := range repos {
			r := repos[i]
			_ = grp.Spinner(ui.PreserveCase(r.Name), func() error {
				probes[i] = gatherRepoProbe(ctx, g, host, r)
				return nil
			})
			emitProbeRows(grp, ui.PreserveCase(r.Name), classifyRepo(probes[i]))
		}
	})

	// Group is closed; the readiness card is on screen with its rows
	// resolved. Re-run classify so the return value and gate decision
	// have the canonical data structures (classify is pure + cheap).
	repoResults, wsFindings, worstSeverity := classifyAll(probes, workspaceProbe)
	anyBlock := worstSeverity == findingBlock
	anyWarn := worstSeverity == findingWarn

	// Hard BLOCK path — no prompt, exit with an actionable error.
	if anyBlock && !force {
		return repoResults, wsFindings, fmt.Errorf("cleanup readiness: blocking findings present; re-run with --force to override")
	}

	// SAFE path — readiness card already on screen, nothing to gate.
	if !anyBlock && !anyWarn {
		return repoResults, wsFindings, nil
	}

	// WARN (or --force with BLOCKs) → Continue / Cancel via Dialog.
	// Dialog rewinds itself on either answer; the readiness card stays
	// as the durable record of what was checked.
	confirmed, err := NewDialog("Warning").
		Description("Not all readiness checks passed, continue anyway?").
		Affirmative("Continue").
		Negative("Cancel").
		Default(false).
		Show()
	if err != nil {
		return nil, nil, err
	}
	if !confirmed {
		return nil, nil, ErrCancelled
	}
	return repoResults, wsFindings, nil
}

// emitProbeRows renders one row per finding (or a single ✓ row if
// none) under a parent group. The row's severity drives the reporter
// method: BLOCK → FailValue (✗), WARN → SkipValue (▲), anything else
// → CompleteValue (✓ with detail). A no-findings probe emits a bare
// ✓ <label> row.
func emitProbeRows(grp ui.Reporter, label string, findings []cleanupFinding) {
	if len(findings) == 0 {
		grp.Complete(label)
		return
	}
	for _, f := range findings {
		switch f.severity {
		case findingBlock:
			grp.FailValue(label, f.message)
		case findingWarn:
			grp.SkipValue(label, f.message)
		default:
			grp.CompleteValue(label, f.message)
		}
	}
}

// emitWorkspaceRows renders the workspace probe's findings as group
// children. The label is "<workspace>" with angle-brackets so the row
// reads as a category-umbrella marker, not another identifier-shaped
// repo row, while keeping the same visual styling as its sibling rows
// (uniform bold purple title across the list). Wrapped with
// PreserveCase so the brackets and lowercase survive titleCase.
func emitWorkspaceRows(grp ui.Reporter, findings []cleanupFinding) {
	label := ui.PreserveCase("<workspace>")
	emitProbeRows(grp, label, findings)
}

// gatherRepoProbe fans out the per-repo probes concurrently. Each
// probe failure is captured into the probe struct (pr=nil + hostErr,
// unset *Known flags, zero values) rather than returned as an error —
// a single transient hiccup shouldn't tank the whole gather, but
// classifyRepo surfaces the gap as an unverified finding instead of
// letting a failed probe silently read as SAFE.
func gatherRepoProbe(ctx context.Context, g vcs.VCS, host code.Host, r Repository) repoCleanupProbe {
	p := repoCleanupProbe{repo: r}

	branch, err := g.GetCurrentBranch(ctx, r.Path)
	if err == nil {
		p.branch = branch
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		dirty, err := g.IsDirty(ctx, r.Path)
		if err == nil {
			p.dirty = dirty
			p.dirtyKnown = true
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		p.headSHA, _ = g.HeadSHA(ctx, r.Path)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sync, err := g.GetBranchSync(ctx, r.Path, p.branch)
		if err == nil {
			p.branchSync = sync
			p.syncKnown = true
		}
	}()

	// Default branch + merge-ancestry lookup. Runs serially because
	// IsMergedInto needs the base ref the first call resolves.
	wg.Add(1)
	go func() {
		defer wg.Done()
		base, err := g.GetDefaultBranch(ctx, r.Path)
		if err != nil {
			return
		}
		baseRef := "origin/" + base
		merged, err := g.IsMergedInto(ctx, r.Path, p.branch, baseRef)
		if err != nil {
			return
		}
		p.isMerged = merged
		p.isMergedKnown = true
	}()

	// PR lookup — host may be nil (no code host configured); treat
	// that as "no PR" rather than a host-unreachable warning.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if host == nil {
			return
		}
		identity, err := gh.ParseRemote(ctx, r.Path)
		if err != nil {
			p.hostErr = err
			return
		}
		pr, err := host.GetPRForBranch(ctx, identity.Owner, identity.Name, p.branch)
		if err != nil {
			p.hostErr = err
			return
		}
		if pr.Number == 0 {
			return // No PR record — leaves p.pr nil.
		}
		p.pr = &pr
	}()

	wg.Wait()
	return p
}

// gatherWorkspaceProbe collects the signals that aren't per-repo:
// stray files in the workspace directory (which RemoveAll would
// destroy) and the issue tracker's status (a lifecycle loose end).
func gatherWorkspaceProbe(ctx context.Context, tracker issue.Tracker, repos []Repository, wsPath, issueKey string) workspaceCleanupProbe {
	p := workspaceCleanupProbe{}

	// Stray files — walk the workspace dir and flag anything that
	// isn't one of the repo subdirs.
	if wsPath != "" {
		entries, err := os.ReadDir(wsPath)
		if err == nil {
			repoNames := make(map[string]bool, len(repos))
			for _, r := range repos {
				repoNames[r.Name] = true
			}
			for _, e := range entries {
				// Skip the repo worktree subdirs and machine-generated OS
				// junk (.DS_Store etc.) — the latter isn't user data worth
				// blocking a cleanup over, and lives outside any repo so
				// git's ignore never reaches it.
				if repoNames[e.Name()] || fsutil.IgnorableName(e.Name()) {
					continue
				}
				p.strayFiles = append(p.strayFiles, e.Name())
			}
			sort.Strings(p.strayFiles)
		}
	}

	// Issue status — short deadline so a flaky tracker doesn't
	// bottleneck the gather. Timeout / error → emit nothing; the
	// issue check is the softest signal and a missing one is fine.
	if tracker != nil && issueKey != "" {
		issueCtx, cancel := context.WithTimeout(ctx, cleanupReadinessProbeTimeout)
		defer cancel()
		if detail, err := tracker.GetIssue(issueCtx, issueKey); err == nil {
			p.issueStatus = detail.Status
			p.issueDoneLike = lifecycleKeyForStatus(detail.Status) == "done"
		}
	}

	return p
}

// classifyAll runs classification across every repo + the workspace
// probe, and returns the worst severity seen anywhere. Split out so
// the raw-mode path and the interactive path share the same logic.
func classifyAll(probes []repoCleanupProbe, ws workspaceCleanupProbe) ([]repoCleanup, []cleanupFinding, findingSeverity) {
	results := make([]repoCleanup, 0, len(probes))
	worst := findingSafe
	for _, p := range probes {
		rc := repoCleanup{repo: p.repo, branch: p.branch, findings: classifyRepo(p)}
		for _, f := range rc.findings {
			if f.severity > worst {
				worst = f.severity
			}
		}
		results = append(results, rc)
	}
	wsFindings := classifyWorkspace(ws)
	for _, f := range wsFindings {
		if f.severity > worst {
			worst = f.severity
		}
	}
	return results, wsFindings, worst
}

// buildCleanupReadinessCard renders the Cleanup Readiness card: one Item
// row per finding, sorted by severity (worst first), within severity
// sorted by source (workspace first, then repo name). Repos with no
// findings collapse to a single SAFE row each.
func buildCleanupReadinessCard(repos []repoCleanup, wsFindings []cleanupFinding) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphWarn := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("▲")
	glyphBlock := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render("✗")

	glyphFor := func(s findingSeverity) string {
		switch s {
		case findingBlock:
			return glyphBlock
		case findingWarn:
			return glyphWarn
		default:
			return glyphOK
		}
	}

	type row struct {
		severity findingSeverity
		source   string // "workspace" or repo name; drives secondary sort
		glyph    string
		content  string
	}
	var rows []row

	for _, f := range wsFindings {
		rows = append(rows, row{
			severity: f.severity,
			source:   "",
			glyph:    glyphFor(f.severity),
			content:  repoStyle.Render("workspace") + muted.Render(" · "+f.message),
		})
	}

	sortedRepos := append([]repoCleanup{}, repos...)
	sort.Slice(sortedRepos, func(i, j int) bool { return sortedRepos[i].repo.Name < sortedRepos[j].repo.Name })

	for _, rc := range sortedRepos {
		if len(rc.findings) == 0 {
			rows = append(rows, row{
				severity: findingSafe,
				source:   rc.repo.Name,
				glyph:    glyphOK,
				content:  repoStyle.Render(rc.repo.Name),
			})
			continue
		}
		for _, f := range rc.findings {
			rows = append(rows, row{
				severity: f.severity,
				source:   rc.repo.Name,
				glyph:    glyphFor(f.severity),
				content:  repoStyle.Render(rc.repo.Name) + muted.Render(" · "+f.message),
			})
		}
	}

	// Worst-first across the whole list. Stable within severity keeps
	// the source-order we already established (workspace rows first
	// at any tier, then repos alphabetically).
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].severity > rows[j].severity })

	state := ui.CardSuccess
	for _, r := range rows {
		if r.severity == findingBlock {
			state = ui.CardFailed
			break
		}
		if r.severity == findingWarn {
			state = ui.CardSkipped
		}
	}

	card := ui.NewCard(state, "cleanup readiness")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
	}
	return card
}

