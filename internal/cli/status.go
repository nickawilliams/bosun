package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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

// newStatusCmd is the scope-aware status command. The annotation
// title is set dynamically per scope in RunE before the root card
// renders (Workspace Status / Project Status), so the breadcrumb
// reflects the current scope.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what wants your attention at workspace or project scope",
		Annotations: map[string]string{
			headerAnnotationTitle: "Status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// Scope detection: workspace if CWD is inside one, else
			// project if inside a project's bosun config, else error.
			projectRoot := config.FindProjectRoot()
			if projectRoot == "" {
				return fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			if wsName, derr := mgr.DetectWorkspace(cwd); derr == nil {
				cmd.Annotations[headerAnnotationTitle] = "Workspace Status"
				return runStatusWorkspace(ctx, cmd, mgr, wsName)
			}

			cmd.Annotations[headerAnnotationTitle] = "Project Status"
			return runStatusProject(ctx, cmd, projectRoot)
		},
	}

	addIssueFlag(cmd)
	return cmd
}

// runStatusWorkspace renders the workspace-scope output — issue
// header card with KV body, then one card per repo with body rows
// (Branch / Checks / PR), then a summary recap.
func runStatusWorkspace(ctx context.Context, cmd *cobra.Command, mgr *workspace.Manager, wsName string) error {
	// Root card — project + issue absorb into the breadcrumb.
	ui.NewCard(ui.CardRoot, commandBreadcrumb(cmd)).Print()

	if name := projectDisplayName(); name != "" {
		ui.NewCard(ui.CardInfo, name).
			HideAbsorbedGlyph().
			AbsorbedTitleColor(ui.Palette.Success).
			ChainAbsorption().
			Print()
	}

	// Issue card — async fetch via RunCardReplace. Spinner shows
	// chained after the project name; on completion the issue card
	// (with KV body of facets) absorbs as the next data segment and
	// renders below the breadcrumb. The preview-env binding is
	// fetched in parallel so it lands inside the same spinner.
	tracker, _ := newIssueTracker()
	previewProvider, _ := newPreviewProvider()
	issueKey, _ := resolveIssue(cmd)
	if tracker != nil && issueKey != "" {
		var (
			detail     issuepkg.Issue
			previewEnv preview.Environment
			previewErr error
		)
		_ = ui.RunCardReplace("", func() error {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, e := tracker.GetIssue(ctx, issueKey)
				if e == nil {
					detail = d
				}
			}()
			if previewProvider != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					previewEnv, previewErr = previewProvider.Get(ctx, issueKey)
				}()
			}
			wg.Wait()
			return nil
		}, func() *ui.Card {
			return buildWorkspaceIssueCard(detail, wsName, previewEnv, previewErr)
		})
	}

	// Per-repo data: gather workspace status, then for each repo
	// run a per-card spinner that fetches sync state + PR + checks
	// and resolves into the full repo card.
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
func runStatusProject(ctx context.Context, cmd *cobra.Command, projectRoot string) error {
	mgr, err := newWorkspaceManager()
	if err != nil {
		return err
	}

	// Enumerate workspaces (cheap — local filesystem walk).
	wsNames, err := mgr.List()
	if err != nil {
		ui.NewCard(ui.CardRoot, commandBreadcrumb(cmd)).Print()
		ui.Skip(fmt.Sprintf("listing workspaces: %v", err))
		return nil
	}

	// Print root card. Project name is absorbed as a breadcrumb data
	// segment by the project header card below; ChainAbsorption arms
	// the next card (the loading-spinner success card) to also absorb,
	// so during loading the spinner shows chained after the project
	// name and disappears when the success card replaces with empty
	// title (and the project Repos body renders below).
	ui.NewCard(ui.CardRoot, commandBreadcrumb(cmd)).Print()

	repos := projectRepos()
	if name := projectDisplayName(); name != "" {
		ui.NewCard(ui.CardInfo, name).
			HideAbsorbedGlyph().
			AbsorbedTitleColor(ui.Palette.Success).
			ChainAbsorption().
			Print()
	}

	// Parallel fetch of all workspaces during a single overall
	// spinner. Empty success-card title means no new breadcrumb
	// segment; success-card body carries the project's Repos KV.
	results := make([]workspaceState, len(wsNames))
	tracker, _ := newIssueTracker()
	host, _ := newCodeHost()
	previewProvider, _ := newPreviewProvider()
	g := git.New()
	if len(wsNames) > 0 {
		_ = ui.RunCardReplace("", func() error {
			var wg sync.WaitGroup
			for i, name := range wsNames {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[i] = fetchWorkspaceState(ctx, mgr, g, host, tracker, previewProvider, name)
				}()
			}
			wg.Wait()
			return nil
		}, func() *ui.Card {
			card := ui.NewCard(ui.CardSuccess, "").HideAbsorbedGlyph()
			if len(repos) > 0 {
				reposLines := projectRepoColumns(repos, ui.TermWidth()-12, 2, 4)
				card.KV("Repos", strings.Join(reposLines, "\n"))
			}
			return card
		})
	} else {
		// No workspaces — still need the project header rendered.
		card := ui.NewCard(ui.CardSuccess, "").HideAbsorbedGlyph()
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
	}

	renderProjectSummary(results)
	_ = projectRoot
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
}

type workspaceRepoCounts struct {
	repos                                int
	done, ready, blocked, pending, broken int
}

// fetchWorkspaceState gathers everything needed to render one
// workspace card at project scope: its issue, its repos, the per-
// repo state, and the aggregate rollup. Errors are swallowed —
// partial fetch leaves fields zero so the row still renders.
func fetchWorkspaceState(ctx context.Context, mgr *workspace.Manager, g vcs.VCS, host code.Host, tracker issuepkg.Tracker, previewProvider preview.Provider, wsName string) workspaceState {
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
	if previewProvider != nil && ws.issueKey != "" {
		ws.previewEnv, ws.previewErr = previewProvider.Get(ctx, ws.issueKey)
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

	card := ui.NewCard(ws.rollup, title).
		PreserveCase().
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

	reposGlyph := statusStyledGlyph(statusStateGlyph(ws.rollup))
	card.Item(reposGlyph, statusRowKV("Repos", workspaceRepoBreakdown(ws.counts)))

	return card
}

// workspaceRepoBreakdown composes a colored single-line summary of
// the workspace's repos. Same vocabulary and color story as the
// project-level summary, just scoped to one workspace.
func workspaceRepoBreakdown(c workspaceRepoCounts) string {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	successStyle := lipgloss.NewStyle().Foreground(ui.Palette.Success)
	warningStyle := lipgloss.NewStyle().Foreground(ui.Palette.Warning)
	errorStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)
	infoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Info)

	parts := []string{
		muted.Render(fmt.Sprintf("%d repos", c.repos)),
	}
	if c.done > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d done", c.done)))
	}
	if c.ready > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d ready", c.ready)))
	}
	if c.blocked > 0 {
		parts = append(parts, warningStyle.Render(fmt.Sprintf("%d blocked", c.blocked)))
	}
	if c.pending > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("%d pending", c.pending)))
	}
	if c.broken > 0 {
		parts = append(parts, errorStyle.Render(fmt.Sprintf("%d broken", c.broken)))
	}
	return strings.Join(parts, muted.Render(", "))
}

// renderProjectSummary prints the muted recap card at the bottom of
// project status — single-line tally of workspaces by their resolved
// state.
func renderProjectSummary(states []workspaceState) {
	successStyle := lipgloss.NewStyle().Foreground(ui.Palette.Success)
	warningStyle := lipgloss.NewStyle().Foreground(ui.Palette.Warning)
	errorStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)
	infoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Info)
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

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

	parts := []string{
		mutedStyle.Render(fmt.Sprintf("%d workspaces", len(states))),
	}
	if done > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d done", done)))
	}
	if ready > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d ready", ready)))
	}
	if blocked > 0 {
		parts = append(parts, warningStyle.Render(fmt.Sprintf("%d blocked", blocked)))
	}
	if pending > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("%d pending", pending)))
	}
	if broken > 0 {
		parts = append(parts, errorStyle.Render(fmt.Sprintf("%d broken", broken)))
	}

	ui.NewCard(ui.CardInfo, strings.Join(parts, ", ")).
		PreserveCase().
		GlyphColor(ui.Palette.Muted).
		Print()
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


// buildWorkspaceIssueCard constructs the issue header card with KV
// body (Type / bold-title, Status, Preview, Workspace branch). Used
// as the success-card finalizer for the issue fetch's RunCardReplace.
func buildWorkspaceIssueCard(detail issuepkg.Issue, branch string, previewEnv preview.Environment, previewErr error) *ui.Card {
	boldTitle := lipgloss.NewStyle().Bold(true).Render(detail.Title)

	workspacePath := workspaceFilesystemPath(branch)
	workspaceValue := branch
	if workspacePath != "" {
		workspaceValue = osc8Link("file://"+workspacePath, branch)
	}

	idDisplay := detail.Key
	if detail.URL != "" {
		idDisplay = osc8Link(detail.URL, detail.Key)
	}

	typeLabel := detail.Type
	if typeLabel == "" {
		typeLabel = "Issue"
	}

	return ui.NewCard(ui.CardSuccess, idDisplay).
		HideAbsorbedGlyph().
		AbsorbedTitleColor(ui.Palette.Success).
		KV(
			typeLabel, boldTitle,
			"Status", detail.Status,
			"Preview", statusPreviewValue(previewEnv, previewErr),
			"Workspace", workspaceValue,
		)
}

// repoState bundles all the per-repo data fetched during status
// loading. Passed from the loading goroutine to the success-card
// finalizer.
type repoState struct {
	sync   vcs.BranchSync
	pr     code.PullRequest
	checks code.CheckRollup

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
func buildWorkspaceRepoCard(s workspace.RepositoryStatus, rs repoState) *ui.Card {
	branchState := branchStateString(rs.sync)
	state := resolveRepoCardState(branchState, rs.pr)

	branchGlyph, branchValue := statusBranchRow(s.Branch, rs.branchURL, rs.sync, s.Dirty)
	checksGlyph, checksValue := statusChecksRow(rs.checks, rs.checksURL)
	prGlyph, prValue := statusPRRow(rs.pr)

	return ui.NewCard(state, s.Name).
		PreserveCase().
		TitleColor(statusCardStateColor(state)).
		Item(branchGlyph, statusRowKV("Branch", branchValue)).
		Item(checksGlyph, statusRowKV("Checks", checksValue)).
		Item(prGlyph, statusRowKV("PR", prValue))
}

// renderWorkspaceSummary prints the muted recap card at the bottom
// — single-line tally of repos by their resolved state.
func renderWorkspaceSummary(states []repoState) {
	successStyle := lipgloss.NewStyle().Foreground(ui.Palette.Success)
	warningStyle := lipgloss.NewStyle().Foreground(ui.Palette.Warning)
	errorStyle := lipgloss.NewStyle().Foreground(ui.Palette.Error)
	infoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Info)
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

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

	parts := []string{
		mutedStyle.Render(fmt.Sprintf("%d repos", len(states))),
	}
	if done > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d done", done)))
	}
	if ready > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d ready", ready)))
	}
	if blocked > 0 {
		parts = append(parts, warningStyle.Render(fmt.Sprintf("%d blocked", blocked)))
	}
	if pending > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("%d pending", pending)))
	}
	if broken > 0 {
		parts = append(parts, errorStyle.Render(fmt.Sprintf("%d broken", broken)))
	}

	ui.NewCard(ui.CardInfo, strings.Join(parts, ", ")).
		PreserveCase().
		GlyphColor(ui.Palette.Muted).
		Print()
}

// projectDisplayName returns the project's display name for the
// breadcrumb. Prefers an explicit `project.name` config value when
// set; falls back to the basename of the project root directory
// (matches how a developer would refer to it conversationally).
// Returns empty string when not in a project.
func projectDisplayName() string {
	if name := strings.TrimSpace(viper.GetString("project.name")); name != "" {
		return name
	}
	if root := config.FindProjectRoot(); root != "" {
		return filepath.Base(root)
	}
	return ""
}

// workspaceFilesystemPath returns the absolute filesystem path for a
// workspace branch, derived the same way other commands resolve it
// (workspace_root + branch, joined against project root for relative
// roots). Empty string when not in a project.
func workspaceFilesystemPath(branch string) string {
	wsRoot := viper.GetString("workspace_root")
	if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}
	if wsRoot == "" {
		return ""
	}
	return filepath.Join(wsRoot, branch)
}

