package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/config"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newStatusCmd is the scope-aware status command. A title resolver
// renders "Workspace Status" or "Project Status" in the breadcrumb
// based on the resolved scope, so the header reflects what the
// command will actually show.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what wants your attention at workspace or project scope",
		RunE: shellRunE(func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cc := commandContext(cmd)

			projectRoot := config.FindProjectRoot()
			if projectRoot == "" {
				return fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
			}

			query, err := resolveWorkspaceQuery(cmd)
			if err != nil {
				return err
			}
			// Status reads at the widest scope its context allows for
			// free, so project scope is implicit outside a workspace;
			// --all makes it explicit (the only way to get the project
			// view from inside a workspace).
			projectScope, err := resolveWorkspaceScope(cmd, cc.Workspace == "", query)
			if err != nil {
				return err
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			if !projectScope {
				return runStatusWorkspace(ctx, cc, mgr)
			}
			return runStatusProject(ctx, query)
		}),
	}

	setTitleResolver(cmd, func(cc CommandContext) string {
		if all, _ := cmd.Flags().GetBool("all"); !all && cc.Workspace != "" {
			return "Workspace Status"
		}
		return "Project Status"
	})

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	addAllFlag(cmd)
	addWorkspaceFilterFlags(cmd)

	return cmd
}

// runStatusWorkspace renders the workspace-scope output — issue
// header card with KV body, then one card per repo with body rows
// (Branch / Checks / PR), then a summary recap.
func runStatusWorkspace(ctx context.Context, cc CommandContext, mgr *workspace.Manager) error {
	wsName := cc.Workspace
	issueKey := cc.Issue

	// Enumerate repos first — cheap local call, needed up front so the
	// issue card's Updated row can show the workspace's last-activity
	// timestamp (max commit time across repos) inside the same fetch.
	statuses, err := mgr.Status(ctx, wsName)
	if err != nil {
		ui.Skip(fmt.Sprintf("workspace status: %v", err))
		return nil
	}
	if len(statuses) == 0 {
		ui.Skip("no repositories found in workspace " + wsName)
		return nil
	}

	host, _ := newCodeHost()
	g := git.New()

	// Meta block — the Status + issue cards render inside the shared
	// preamble helper (single tracker fetch feeds both); the
	// preview-env binding and last-activity timestamp are fetched
	// alongside the issue so they land inside the same spinner.
	tracker, _ := newIssueTracker()
	if tracker != nil && issueKey != "" {
		var (
			previewEnv preview.Environment
			previewErr error
			updatedAt  time.Time
			updatedMu  sync.Mutex
		)
		// A failed provider construction renders "(unavailable)" via
		// previewErr — "(none)" would misread a misconfigured provider
		// as "no env bound".
		previewProvider, provErr := newPreviewProvider(wsName)
		if previewProvider == nil && provErr != nil {
			previewErr = provErr
		}
		_, _ = emitWorkspaceIssuePreamble(ctx, issueKey, func() {
			var wg sync.WaitGroup
			if previewProvider != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					previewEnv, previewErr = previewProvider.Get(ctx, issueKey)
				}()
			}
			// Fan out per-repo last-commit lookups in parallel — local
			// git calls, cheap and concurrent-safe.
			for _, s := range statuses {
				s := s
				wg.Add(1)
				go func() {
					defer wg.Done()
					t, err := g.LastCommitTime(ctx, s.Path, s.Branch)
					if err != nil {
						return
					}
					updatedMu.Lock()
					if t.After(updatedAt) {
						updatedAt = t
					}
					updatedMu.Unlock()
				}()
			}
			wg.Wait()
		})
		// Order: Story → Preview → Workspace. Now that each meta card
		// uses the title-plus-body layout (no inline " · value" to
		// align), they read better separated than stacked tight — so
		// none are Tight, and the normal connector break sits between
		// each, matching the spacing of the per-repo cards below.
		buildWorkspacePreviewCard(previewEnv, previewErr).Print()
		buildWorkspaceBranchCard(wsName, updatedAt).Print()
	}

	// Per-repo cards.
	resolved := make([]repoState, 0, len(statuses))
	for _, s := range statuses {
		var rs repoState
		_ = ui.RunCardThen(s.Name, func() error {
			rs = fetchRepoState(ctx, g, host, s)
			return nil
		}, func() *ui.Card {
			return buildWorkspaceRepoCard(s, rs)
		})
		resolved = append(resolved, rs)
	}

	renderWorkspaceSummary(resolved)
	return nil
}

// runStatusProject renders the project-scope output — project
// header card with KV body (Repos columnar list), then one card
// per workspace (sorted by lifecycle position) with body rows for
// Status and the Repos rollup, then a summary recap. An active query
// narrows the cards to matching workspaces; workspaces the query
// can't evaluate are reported as skips rather than silently dropped.
//
// Loading strategy diverges from workspace scope: project uses a
// single overall spinner during a parallel fetch of all workspaces,
// then renders cards in sorted order. Per-workspace sequential
// spinners (the workspace-scope pattern) wouldn't allow lifecycle
// sorting since rendering would happen in fetch order. The trade
// is intentional — at project scope the user wants a sorted triage
// overview more than progressive per-workspace disclosure.
func runStatusProject(ctx context.Context, query workspaceQuery) error {
	mgr, err := newWorkspaceManager()
	if err != nil {
		return err
	}

	// Enumerate workspaces (cheap — local filesystem walk).
	wsNames, err := mgr.List()
	if err != nil {
		ui.Skip(fmt.Sprintf("listing workspaces: %v", err))
		return nil
	}

	// Host first: the project's repo links come off it, and it is
	// needed for the per-workspace fetch below regardless.
	tracker, _ := newIssueTracker()
	host, _ := newCodeHost()
	repos := projectRepos(host)

	// Parallel fetch of all workspaces during a single overall
	// spinner. Empty success-card title means no new breadcrumb
	// segment; success-card body carries the project's Repos KV.
	var results []workspaceState
	g := git.New()
	if len(wsNames) > 0 {
		_ = ui.RunCardReplace("", func() error {
			results = observeWorkspaces(ctx, wsNames, func(ctx context.Context, name string) workspaceState {
				return fetchWorkspaceState(ctx, mgr, g, host, tracker, name)
			})
			return nil
		}, func() *ui.Card {
			card := ui.NewCard(ui.CardSuccess, "")
			if len(repos) > 0 {
				reposLines := projectRepoColumns(repos, ui.TermWidth()-12, 2, 4)
				card.KV("Repos", strings.Join(reposLines, "\n"))
			}
			return card
		})
	} else {
		// No workspaces — still need the project header rendered.
		card := ui.NewCard(ui.CardSuccess, "")
		if len(repos) > 0 {
			reposLines := projectRepoColumns(repos, ui.TermWidth()-12, 2, 4)
			card.KV("Repos", strings.Join(reposLines, "\n"))
		}
		card.Print()
		ui.Skip("no workspaces found in project")
		return nil
	}

	// Narrow to the query's matches (unevaluable workspaces surface
	// as skips inside filterWorkspaces).
	observed := len(results)
	if query.active() {
		results = filterWorkspaces(results, query)
		if len(results) == 0 {
			ui.Skip("no workspaces match the filter")
			return nil
		}
	}

	// Sort by lifecycle position using the canonical bosun lifecycle
	// vocabulary (lifecycleStatusKeys + "done"). Statuses that don't
	// resolve to a configured lifecycle stage sort after "done".
	// Tie-break by issue key for stable ordering.
	idx := buildStatusIndex()
	end := len(idx) // unmapped statuses sort after the last known one
	sort.SliceStable(results, func(i, j int) bool {
		oi, ok := idx[strings.ToLower(results[i].issue.Status)]
		if !ok {
			oi = end
		}
		oj, ok := idx[strings.ToLower(results[j].issue.Status)]
		if !ok {
			oj = end
		}
		if oi != oj {
			return oi < oj
		}
		return results[i].issueKey < results[j].issueKey
	})

	for _, ws := range results {
		buildProjectWorkspaceCard(ws).Print()
	}

	renderProjectSummary(results)
	if query.active() && len(results) < observed {
		ui.Info("showing %d of %d workspaces", len(results), observed)
	}

	return nil
}

// workspaceState bundles all the per-workspace data fetched during
// project status loading.
type workspaceState struct {
	name       string              // workspace name (filesystem-relative path)
	issueKey   string              // issue key extracted from workspace name
	issueURL   string              // tracker URL for the issue
	issue      issuepkg.Issue      // tracker-fetched details (Type, Title, Status)
	repos      []repoState         // per-repo states (sync, pr, checks)
	repoNames  []string            // repo names parallel to repos slice
	rollup     ui.CardState        // aggregate state across repos
	counts     workspaceRepoCounts // per-bucket repo tally
	previewEnv preview.Environment // preview-env binding (Name/URL/Alive/Probed)
	previewErr error               // ErrNoEnvironment, ProbeError, or other

	// updatedAt is the most recent commit time across the workspace's
	// repos. Zero value when no repo lookup succeeded (workspace will
	// render without an Updated row).
	updatedAt time.Time
}

type workspaceRepoCounts struct {
	repos                                 int
	done, ready, blocked, pending, broken int
}

// fetchWorkspaceIssueState is the issue-only observation of one
// workspace: its name, the issue key derived from the name, and the
// tracker's detail for it. It is what a workspaceQuery needs to
// evaluate a workspace and nothing more — cleanup's bulk filter
// observes with this instead of the full fetch so filtering doesn't
// fan out per-repo host calls it never uses. Errors are swallowed:
// a failed tracker fetch leaves issue zero, which the query reports
// as unevaluable rather than treating as a non-match.
func fetchWorkspaceIssueState(ctx context.Context, tracker issuepkg.Tracker, wsName string) workspaceState {
	ws := workspaceState{name: wsName}

	// Issue key from workspace name (e.g., "feature/EX-30434_foo" →
	// "EX-30434"). Try the trailing path segment first; fall back
	// to the whole name.
	ws.issueKey = extractIssue(filepath.Base(wsName))
	if ws.issueKey == "" {
		ws.issueKey = extractIssue(wsName)
	}

	// Tracker fetch. Skip silently if no tracker or no issue key.
	if tracker != nil && ws.issueKey != "" {
		if detail, err := tracker.GetIssue(ctx, ws.issueKey); err == nil {
			ws.issue = detail
			ws.issueURL = detail.URL
		}
	}

	return ws
}

// fetchWorkspaceState gathers everything needed to render one
// workspace card at project scope: its issue, its repos, the per-
// repo state, and the aggregate rollup. Errors are swallowed —
// partial fetch leaves fields zero so the row still renders.
func fetchWorkspaceState(ctx context.Context, mgr *workspace.Manager, g vcs.VCS, host code.Host, tracker issuepkg.Tracker, wsName string) workspaceState {
	ws := fetchWorkspaceIssueState(ctx, tracker, wsName)

	// Preview-env binding. ErrNoEnvironment is the empty-state signal;
	// ProbeError carries a partial Environment we still want to render.
	if ws.issueKey != "" {
		if provider, err := newPreviewProvider(wsName); err == nil && provider != nil {
			ws.previewEnv, ws.previewErr = provider.Get(ctx, ws.issueKey)
		}
	}

	// Per-repo states for the rollup.
	statuses, err := mgr.Status(ctx, wsName)
	if err != nil {
		return ws
	}
	ws.counts.repos = len(statuses)
	for _, s := range statuses {
		rs := fetchRepoState(ctx, g, host, s)
		ws.repos = append(ws.repos, rs)
		ws.repoNames = append(ws.repoNames, s.Name)
		// Take the most recent commit time across all repos as the
		// workspace's last-activity signal.
		if rs.lastCommit.After(ws.updatedAt) {
			ws.updatedAt = rs.lastCommit
		}
		switch resolveRepoCardState(branchStateString(rs.sync), rs.pr, rs.checks) {
		case ui.CardSuccess:
			ws.counts.done++
		case ui.CardReady:
			ws.counts.ready++
		case ui.CardSkipped:
			ws.counts.blocked++
		case ui.CardWaiting:
			ws.counts.pending++
		case ui.CardFailed:
			ws.counts.broken++
		}
	}

	// Rollup state — highest-priority wins per the aggregation rule.
	switch {
	case ws.counts.broken > 0:
		ws.rollup = ui.CardFailed
	case ws.counts.blocked > 0:
		ws.rollup = ui.CardSkipped
	case ws.counts.pending > 0:
		ws.rollup = ui.CardWaiting
	case ws.counts.ready > 0:
		ws.rollup = ui.CardReady
	case ws.counts.done > 0:
		ws.rollup = ui.CardSuccess
	default:
		ws.rollup = ui.CardWaiting
	}
	return ws
}

// buildProjectWorkspaceCard constructs the resolved workspace card
// for project scope: title is the issue ID (linked), value is the
// issue title (state-tinted), body rows are Status / Repos rollup
// (Preview and Updated deferred — need data sources).
func buildProjectWorkspaceCard(ws workspaceState) *ui.Card {
	titleColor := statusCardStateColor(ws.rollup)

	title := ws.issueKey
	if title == "" {
		title = ws.name
	}
	if ws.issueURL != "" {
		title = osc8Link(ws.issueURL, title)
	}

	value := ws.issue.Title
	if value == "" {
		value = ws.name
	}
	styledTitle := lipgloss.NewStyle().Foreground(titleColor).Render(value)

	// State-context card: keep glyph color and title color in sync so
	// the project-scope workspace row reads consistently with the
	// workspace-scope meta block. statusCardStateColor already returns
	// the Role* role color for the resolved CardState.
	card := ui.NewCard(ws.rollup, title).
		PreserveCase().
		GlyphColor(titleColor).
		TitleColor(titleColor).
		Value(styledTitle)

	if ws.issue.Status != "" {
		statusGlyph := statusStyledGlyph(lifecycleKeyGlyph(lifecycleKeyForStatus(ws.issue.Status)))
		card.Item(statusGlyph, statusRowKV("Status", ws.issue.Status))
	}

	// Preview row — skip when no env bound. At project scope, cards
	// are dense; "(none)" rows add visual noise for workspaces that
	// don't use preview envs.
	if pg, pv := statusPreviewRow(ws.previewEnv, ws.previewErr); pg != "" {
		card.Item(pg, statusRowKV("Preview", pv))
	}

	// Updated row — most recent commit across repos, bucketed into
	// staleness levels. Skip when no timestamp was captured.
	if ug, uv := statusUpdatedRow(ws.updatedAt); ug != "" {
		card.Item(ug, statusRowKV("Updated", uv))
	}

	reposGlyph := statusStyledGlyph(statusStateGlyph(ws.rollup))
	card.Item(reposGlyph, statusRowKV("Repos", workspaceRepoBreakdown(ws.counts)))

	return card
}

// pluralize returns singular when n == 1, otherwise plural. Keeps
// summary headings grammatically correct ("1 repository" vs.
// "3 repositories").
func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

// workspaceRepoBreakdown composes a colored single-line summary of
// the workspace's repos. Same vocabulary and color story as the
// project-level summary, just scoped to one workspace.
//
// State-context call site — each label is a state bucket, so colors
// route through the Role* palette aliases (see state_grammar.go).
func workspaceRepoBreakdown(c workspaceRepoCounts) string {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.RoleNeutral)
	doneStyle := lipgloss.NewStyle().Foreground(ui.Palette.RoleDone)
	openStyle := lipgloss.NewStyle().Foreground(ui.Palette.RoleOpen)
	attentionStyle := lipgloss.NewStyle().Foreground(ui.Palette.RoleAttention)
	closedStyle := lipgloss.NewStyle().Foreground(ui.Palette.RoleClosed)
	inFlightStyle := lipgloss.NewStyle().Foreground(ui.Palette.RoleInFlight)

	parts := []string{
		muted.Render(fmt.Sprintf("%d %s", c.repos, pluralize(c.repos, "repository", "repositories"))),
	}
	if c.done > 0 {
		parts = append(parts, doneStyle.Render(fmt.Sprintf("%d done", c.done)))
	}
	if c.ready > 0 {
		parts = append(parts, openStyle.Render(fmt.Sprintf("%d ready", c.ready)))
	}
	if c.blocked > 0 {
		parts = append(parts, attentionStyle.Render(fmt.Sprintf("%d blocked", c.blocked)))
	}
	if c.pending > 0 {
		parts = append(parts, inFlightStyle.Render(fmt.Sprintf("%d pending", c.pending)))
	}
	if c.broken > 0 {
		parts = append(parts, closedStyle.Render(fmt.Sprintf("%d broken", c.broken)))
	}
	return strings.Join(parts, muted.Render(", "))
}

// renderProjectSummary prints the recap card at the bottom of project
// status — tally of workspaces by their resolved state. Segments are
// ordered ascending by severity (info-ish first, error last) so the
// order-based glyph rollup in Reporter.Summary picks the worst case.
func renderProjectSummary(states []workspaceState) {
	var done, ready, blocked, pending, broken int
	for _, ws := range states {
		switch ws.rollup {
		case ui.CardSuccess:
			done++
		case ui.CardReady:
			ready++
		case ui.CardSkipped:
			blocked++
		case ui.CardWaiting:
			pending++
		case ui.CardFailed:
			broken++
		}
	}

	ui.Default().Summary(
		fmt.Sprintf("%d %s", len(states), pluralize(len(states), "workspace", "workspaces")),
		[]ui.SummarySegment{
			{Count: done, Label: "done", Color: ui.Palette.RoleDone},
			{Count: ready, Label: "ready", Color: ui.Palette.RoleOpen},
			{Count: pending, Label: "pending", Color: ui.Palette.RoleInFlight},
			{Count: blocked, Label: "blocked", Color: ui.Palette.RoleAttention},
			{Count: broken, Label: "broken", Color: ui.Palette.RoleClosed},
		},
	)
}

// projectRepos returns the project's configured repositories with host
// URLs derived from each repo's origin remote when known. A nil host
// (none configured) yields bare names — the same degradation
// fetchRepoState applies to its per-repo links.
func projectRepos(host code.Host) []projectRepoEntry {
	configured, err := resolveRepositories(nil)
	if err != nil {
		return nil
	}
	out := make([]projectRepoEntry, 0, len(configured))
	for _, r := range configured {
		entry := projectRepoEntry{name: r.Name}
		if host != nil {
			if identity, err := host.ParseRemote(context.Background(), r.Path); err == nil {
				entry.url = host.RepositoryURL(identity)
			}
		}
		out = append(out, entry)
	}
	return out
}

// buildWorkspaceStoryCard renders the issue/story meta card in the
// standard title-plus-body layout: the title row carries the issue
// type alone (Story / Bug / Task / etc., "Issue" if unknown); the
// body stacks the bold OSC8-linked issue key + title, a blank
// spacer, then the lifecycle stepper — all at the standard body
// indent, no value-column alignment math.
//
// When the tracker fetch failed (fetchOK false), the card renders
// its degraded variant: ▲ warning glyph, body line with the issue
// key (falling back to issueKey when detail is zero) + muted
// "(title unavailable)", no stepper.
//
// Layout (Tight, ordering) is the caller's concern.
func buildWorkspaceStoryCard(detail issuepkg.Issue, issueKey string, fetchOK bool) *ui.Card {
	typeLabel := detail.Type
	if typeLabel == "" {
		typeLabel = "Issue"
	}

	if !fetchOK {
		key := detail.Key
		if key == "" {
			key = issueKey
		}
		muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
		line := ui.Keyword(key) +
			muted.Render(" · (title unavailable)")
		return ui.NewCard(ui.CardSkipped, typeLabel).Raw(line)
	}

	issueRef := detail.Key
	if detail.URL != "" {
		issueRef = osc8Link(detail.URL, detail.Key)
	}
	issueLine := ui.Keyword(issueRef) + ": " + detail.Title

	key := lifecycleKeyForStatus(detail.Status)
	var stepper string
	if stepperSlotIndex(key) < 0 {
		stepper = renderLifecycleStepperUnmapped(detail.Status)
	} else {
		stepper = renderLifecycleStepper(key)
	}

	body := make([]string, 0, 4)
	body = append(body, issueLine, "")
	body = append(body, strings.Split(stepper, "\n")...)

	return ui.NewCard(ui.CardReady, typeLabel).Raw(body...)
}

// buildWorkspacePreviewCard renders the preview-environment meta
// card in the standard title-plus-body layout: title "Preview", the
// preview URL/name (or a muted "(none)" sentinel from
// statusPreviewValue) on the body line beneath. The text carries the
// existence contrast on its own, so the glyph stays a structural ●
// with no state-color override.
//
// Layout (Tight, ordering) is the caller's concern.
func buildWorkspacePreviewCard(env preview.Environment, err error) *ui.Card {
	return ui.NewCard(ui.CardReady, "Preview").Raw(statusPreviewValue(env, err))
}

// buildWorkspaceBranchCard renders the workspace-branch meta card in
// the standard title-plus-body layout: title "Workspace", and on the
// body line the branch name (file:// linked to its worktree path when
// resolvable) plus a colored age parenthetical at the end.
//
// Renders as a CardReady (●) with the dot color overridden per
// staleness bucket (see [stalenessColor]).
//
// Layout (Tight, ordering) is the caller's concern.
func buildWorkspaceBranchCard(branch string, updatedAt time.Time) *ui.Card {
	dotColor := stalenessColor(updatedAt)
	workspacePath := workspaceFilesystemPath(branch)
	value := branch
	if workspacePath != "" {
		value = osc8Link("file://"+workspacePath, branch)
	}

	if !updatedAt.IsZero() {
		parens := lipgloss.NewStyle().Foreground(dotColor).Render("(" + humanizeAge(time.Since(updatedAt)) + ")")
		value += " " + parens
	}

	return ui.NewCard(ui.CardReady, "Workspace").
		GlyphColor(dotColor).
		Raw(value)
}

// repoState bundles all the per-repo data fetched during status
// loading. Passed from the loading goroutine to the success-card
// finalizer.
type repoState struct {
	sync   vcs.BranchSync
	pr     code.PullRequest
	checks code.CheckRollup

	// lastCommit is the timestamp of the most recent commit on the
	// repo's branch. Zero value when the lookup failed.
	lastCommit time.Time

	// branchURL / checksURL are the host web URLs the row values
	// link to. Empty when the repo's host identity isn't known.
	branchURL string
	checksURL string
}

// fetchRepoState gathers branch sync, PR, and checks for one repo.
// Errors are swallowed — a partial fetch leaves the relevant fields
// empty / zero-valued so the row still renders, just with less data.
func fetchRepoState(ctx context.Context, g vcs.VCS, host code.Host, s workspace.RepositoryStatus) repoState {
	rs := repoState{}

	if sync, err := g.GetBranchSync(ctx, s.Path, s.Branch); err == nil {
		rs.sync = sync
	}

	if t, err := g.LastCommitTime(ctx, s.Path, s.Branch); err == nil {
		rs.lastCommit = t
	}

	if host == nil {
		return rs
	}
	identity, err := host.ParseRemote(ctx, s.Path)
	if err != nil {
		return rs
	}

	rs.branchURL = host.BranchURL(identity, s.Branch)

	pr, err := host.GetPRForBranch(ctx, identity.Owner, identity.Name, s.Branch)
	if err == nil {
		rs.pr = pr
	}

	// Checks: prefer the PR's head SHA when one exists; fall back
	// to the branch ref otherwise.
	ref := s.Branch
	if rs.pr.HeadSHA != "" {
		ref = rs.pr.HeadSHA
	}
	if rollup, err := host.GetChecks(ctx, identity.Owner, identity.Name, ref); err == nil {
		rs.checks = rollup
	}

	// Checks URL — PR's checks tab when a PR exists, otherwise the
	// HEAD commit's checks page.
	if rs.pr.Number > 0 {
		rs.checksURL = pr.URL + "/checks"
	} else if ref != "" {
		rs.checksURL = host.ChecksURL(identity, ref)
	}

	return rs
}

// buildWorkspaceRepoCard constructs the resolved repo card with
// gutter glyph, state-tinted title, and three body rows (Branch /
// Checks / PR). Used as the success-card finalizer for the per-repo
// RunCardThen.
//
// Gutter glyph + title color come from repoCardGlyphVisual, which
// applies the state-context grammar (●  for active states colored
// to role; ✓ purple for terminal merged; ✗ red for terminal closed).
func buildWorkspaceRepoCard(s workspace.RepositoryStatus, rs repoState) *ui.Card {
	branchState := branchStateString(rs.sync)
	state, glyphCol := repoCardGlyphVisual(branchState, rs.pr, rs.checks)

	branchGlyph, branchValue := statusBranchRow(s.Branch, rs.branchURL, rs.sync, s.Dirty)
	checksGlyph, checksValue := statusChecksRow(rs.checks, rs.checksURL)
	prGlyph, prValue := statusPRRow(rs.pr, rs.checks)

	return ui.NewCard(state, s.Name).
		PreserveCase().
		GlyphColor(glyphCol).
		TitleColor(glyphCol).
		Item(branchGlyph, statusRowKV("Branch", branchValue)).
		Item(checksGlyph, statusRowKV("Checks", checksValue)).
		Item(prGlyph, statusRowKV("PR", prValue))
}

// renderWorkspaceSummary prints the recap card at the bottom of
// workspace status — tally of repos by their resolved state.
// Segments are ordered ascending by severity (info-ish first, error
// last) so the order-based glyph rollup in Reporter.Summary picks
// the worst case.
func renderWorkspaceSummary(states []repoState) {
	var done, ready, blocked, pending, broken int
	for _, rs := range states {
		branchState := branchStateString(rs.sync)
		switch resolveRepoCardState(branchState, rs.pr, rs.checks) {
		case ui.CardSuccess:
			done++
		case ui.CardReady:
			ready++
		case ui.CardSkipped:
			blocked++
		case ui.CardWaiting:
			pending++
		case ui.CardFailed:
			broken++
		}
	}

	ui.Default().Summary(
		fmt.Sprintf("%d %s", len(states), pluralize(len(states), "repository", "repositories")),
		[]ui.SummarySegment{
			{Count: done, Label: "done", Color: ui.Palette.RoleDone},
			{Count: ready, Label: "ready", Color: ui.Palette.RoleOpen},
			{Count: pending, Label: "pending", Color: ui.Palette.RoleInFlight},
			{Count: blocked, Label: "blocked", Color: ui.Palette.RoleAttention},
			{Count: broken, Label: "broken", Color: ui.Palette.RoleClosed},
		},
	)
}

// workspaceFilesystemPath returns the absolute filesystem path for a
// workspace branch, derived the same way other commands resolve it
// (workspace.root + branch, joined against project root for relative
// roots). Empty string when not in a project.
func workspaceFilesystemPath(branch string) string {
	wsRoot := viper.GetString("workspace.root")
	if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}
	if wsRoot == "" {
		return ""
	}
	return filepath.Join(wsRoot, branch)
}
