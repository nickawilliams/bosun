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
	gh "github.com/nickawilliams/bosun/internal/code/github"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			cc := commandContext(cmd)

			projectRoot := config.FindProjectRoot()
			if projectRoot == "" {
				return fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			if cc.Workspace != "" {
				return runStatusWorkspace(ctx, cc, mgr)
			}
			return runStatusProject(ctx)
		},
	}

	setTitleResolver(cmd, func(cc CommandContext) string {
		if cc.Workspace != "" {
			return "Workspace Status"
		}
		return "Project Status"
	})

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)

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
	//
	// Provider OnInfo events (stale-metadata cleanup, etc.) buffer
	// here so they emit after the meta block prints, not racing with
	// the loading spinner.
	tracker, _ := newIssueTracker()
	if tracker != nil && issueKey != "" {
		var (
			previewEnv   preview.Environment
			previewErr   error
			updatedAt    time.Time
			updatedMu    sync.Mutex
			infoMessages []previewInfoEvent
		)
		previewProvider, _ := newPreviewProviderWithInfo(wsName, func(action, value string) {
			infoMessages = append(infoMessages, previewInfoEvent{action: action, value: value})
		})
		emitWorkspaceIssuePreamble(ctx, issueKey, func() {
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
		// Order: Status → Story → Preview → Workspace. The helper
		// prints Status and Story; the Story card prints without
		// Tight (lifecycle commands want the comfy break after it),
		// so we suppress the pending spacer manually to keep Preview
		// flush in the meta block. Preview.Tight() chains the
		// suppression into Workspace; Workspace is the last meta
		// card and not Tight, so the trailing blank line separates
		// the meta block from the per-repo cards that follow.
		ui.ClearSpacer()
		buildWorkspacePreviewCard(previewEnv, previewErr).Tight().Print()
		buildWorkspaceBranchCard(wsName, updatedAt).Print()
		// Drain captured events into the timeline now that the meta
		// block has printed.
		for _, msg := range infoMessages {
			ui.NewCard(ui.CardSuccess, msg.action).Value(msg.value).Print()
		}
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
// Status and the Repos rollup, then a summary recap.
//
// Loading strategy diverges from workspace scope: project uses a
// single overall spinner during a parallel fetch of all workspaces,
// then renders cards in sorted order. Per-workspace sequential
// spinners (the workspace-scope pattern) wouldn't allow lifecycle
// sorting since rendering would happen in fetch order. The trade
// is intentional — at project scope the user wants a sorted triage
// overview more than progressive per-workspace disclosure.
func runStatusProject(ctx context.Context) error {
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

	repos := projectRepos()

	// Parallel fetch of all workspaces during a single overall
	// spinner. Empty success-card title means no new breadcrumb
	// segment; success-card body carries the project's Repos KV.
	results := make([]workspaceState, len(wsNames))
	tracker, _ := newIssueTracker()
	host, _ := newCodeHost()
	g := git.New()
	if len(wsNames) > 0 {
		_ = ui.RunCardReplace("", func() error {
			var wg sync.WaitGroup
			for i, name := range wsNames {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[i] = fetchWorkspaceState(ctx, mgr, g, host, tracker, name)
				}()
			}
			wg.Wait()
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
		// Emit any incidental events captured during this workspace's
		// fetch (e.g., stale-metadata cleanup) after the card so the
		// timeline reads card → notice → next card. Card style with
		// title + value keeps action heading-cased and value raw.
		for _, msg := range ws.infoMessages {
			ui.NewCard(ui.CardSuccess, msg.action).Value(msg.value).Print()
		}
	}

	renderProjectSummary(results)

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

	// infoMessages captures incidental events (e.g., "cleared stale
	// metadata") emitted by the preview provider during this
	// workspace's fetch. Buffered here so the command can render
	// them after the workspace's card prints rather than letting
	// them race with the loading spinner.
	infoMessages []previewInfoEvent
}

// previewInfoEvent carries a preview-provider incidental event for
// deferred rendering. Split into action + value so the card render
// can title-case the action and keep the value raw-cased and muted.
type previewInfoEvent struct {
	action string
	value  string
}

type workspaceRepoCounts struct {
	repos                                 int
	done, ready, blocked, pending, broken int
}

// fetchWorkspaceState gathers everything needed to render one
// workspace card at project scope: its issue, its repos, the per-
// repo state, and the aggregate rollup. Errors are swallowed —
// partial fetch leaves fields zero so the row still renders.
//
// The preview provider is constructed per workspace with an OnInfo
// callback that captures incidental events (stale-metadata cleanup,
// etc.) into ws.infoMessages, so the command can emit them after
// the workspace's card prints instead of racing with the spinner.
func fetchWorkspaceState(ctx context.Context, mgr *workspace.Manager, g vcs.VCS, host code.Host, tracker issuepkg.Tracker, wsName string) workspaceState {
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

	// Preview-env binding. ErrNoEnvironment is the empty-state signal;
	// ProbeError carries a partial Environment we still want to render.
	// Build a per-workspace provider whose OnInfo writes into this
	// workspace's buffer (no shared state with other goroutines, since
	// each fetch has its own ws value).
	if ws.issueKey != "" {
		if provider, err := newPreviewProviderWithInfo(wsName, func(action, value string) {
			ws.infoMessages = append(ws.infoMessages, previewInfoEvent{action: action, value: value})
		}); err == nil && provider != nil {
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
		switch resolveRepoCardState(branchStateString(rs.sync), rs.pr) {
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

// projectRepos returns the project's configured repositories with
// GitHub URLs derived from each repo's origin remote when known.
func projectRepos() []projectRepoEntry {
	configured, err := resolveRepositories(nil)
	if err != nil {
		return nil
	}
	out := make([]projectRepoEntry, 0, len(configured))
	for _, r := range configured {
		entry := projectRepoEntry{name: r.Name}
		if identity, err := gh.ParseRemote(context.Background(), r.Path); err == nil {
			entry.url = fmt.Sprintf("https://github.com/%s/%s", identity.Owner, identity.Name)
		}
		out = append(out, entry)
	}
	return out
}

// buildWorkspaceStoryCard renders the issue/story meta card. Title
// is the issue type (Story / Bug / Task / etc., "Issue" if unknown);
// title-line value is the OSC8-linked issue key + bold title; the
// lifecycle stepper appears below as an indented body block, aligned
// with the value column.
//
// When the tracker fetch failed (fetchOK false), the card renders
// its degraded variant: ▲ warning glyph, issue key (falling back to
// issueKey when detail is zero) + muted "(title unavailable)", no
// stepper.
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
		value := lipgloss.NewStyle().Bold(true).Render(key) +
			muted.Render(" · (title unavailable)")
		return ui.NewCard(ui.CardSkipped, typeLabel).
			Value(value).
			AlignWidth(statusMetaAlignWidth)
	}

	issueRef := detail.Key
	if detail.URL != "" {
		issueRef = osc8Link(detail.URL, detail.Key)
	}
	value := lipgloss.NewStyle().Bold(true).Render(issueRef + ": " + detail.Title)

	card := ui.NewCard(ui.CardReady, typeLabel).
		Value(value).
		AlignWidth(statusMetaAlignWidth)

	// Stepper folded into the body, indented to land in the same
	// column as the title-line value. statusMetaAlignWidth + 3
	// matches Card.Value's continuation-line padding so the dot
	// track lines up beneath the title text.
	key := lifecycleKeyForStatus(detail.Status)
	var stepper string
	if stepperSlotIndex(key) < 0 {
		stepper = renderLifecycleStepperUnmapped(detail.Status)
	} else {
		stepper = renderLifecycleStepper(key)
	}
	indent := strings.Repeat(" ", statusMetaAlignWidth+3)
	stepperLines := strings.Split(stepper, "\n")
	indented := make([]string, len(stepperLines))
	for i, l := range stepperLines {
		indented[i] = indent + l
	}

	card.Text("") // blank │ row above the stepper
	card.Raw(indented...)

	return card
}

// buildWorkspacePreviewCard renders the preview-environment meta
// card. Title is "Preview"; value is the preview URL/name or a muted
// "(none)" sentinel produced by statusPreviewValue. The text carries
// the existence contrast on its own, so the glyph stays a structural
// ● with no state-color override.
//
// Layout (Tight, ordering) is the caller's concern.
func buildWorkspacePreviewCard(env preview.Environment, err error) *ui.Card {
	value := statusPreviewValue(env, err)
	return ui.NewCard(ui.CardReady, "Preview").
		Value(value).
		AlignWidth(statusMetaAlignWidth)
}

// buildWorkspaceBranchCard renders the workspace-branch meta card.
// Title is "Workspace"; value is the branch name (file:// linked to
// its worktree path when resolvable) plus a colored age
// parenthetical at the end — same shape as the Story card's status
// parens and the PR row.
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
		Value(value).
		AlignWidth(statusMetaAlignWidth)
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

	// branchURL / checksURL are the GitHub web URLs the row values
	// link to. Empty when the repo's GitHub identity isn't known.
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
	identity, err := gh.ParseRemote(ctx, s.Path)
	if err != nil {
		return rs
	}

	rs.branchURL = fmt.Sprintf("https://github.com/%s/%s/tree/%s", identity.Owner, identity.Name, s.Branch)

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
		rs.checksURL = fmt.Sprintf("https://github.com/%s/%s/commit/%s/checks", identity.Owner, identity.Name, ref)
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
	state, glyphCol := repoCardGlyphVisual(branchState, rs.pr)

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
		switch resolveRepoCardState(branchState, rs.pr) {
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
