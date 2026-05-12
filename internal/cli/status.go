package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/config"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
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

	projectName := strings.TrimSpace(viper.GetString("project.name"))
	if projectName != "" {
		ui.NewCard(ui.CardInfo, projectName).
			HideAbsorbedGlyph().
			AbsorbedTitleColor(ui.Palette.Success).
			ChainAbsorption().
			Print()
	}

	// Issue card — async fetch via RunCardReplace. Spinner shows
	// chained after the project name; on completion the issue card
	// (with KV body of facets) absorbs as the next data segment and
	// renders below the breadcrumb.
	tracker, _ := newIssueTracker()
	issueKey, _ := resolveIssue(cmd)
	if tracker != nil && issueKey != "" {
		var detail issuepkg.Issue
		_ = ui.RunCardReplace("", func() error {
			var e error
			detail, e = tracker.GetIssue(ctx, issueKey)
			return e
		}, func() *ui.Card {
			return buildWorkspaceIssueCard(detail, wsName)
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

// runStatusProject is a stub for project-scope status. Implementation
// follows in a later commit; for now print a placeholder.
func runStatusProject(ctx context.Context, cmd *cobra.Command, projectRoot string) error {
	ui.NewCard(ui.CardRoot, commandBreadcrumb(cmd)).Print()
	ui.NewCard(ui.CardInfo, "Project status not yet implemented").
		Subtitle("Run from inside a workspace for workspace status, or wait for project-scope output to be wired up.").
		Print()
	_ = ctx
	_ = projectRoot
	return nil
}

// buildWorkspaceIssueCard constructs the issue header card with KV
// body (Type / bold-title, Status, Workspace branch). Used as the
// success-card finalizer for the issue fetch's RunCardReplace.
func buildWorkspaceIssueCard(detail issuepkg.Issue, branch string) *ui.Card {
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

