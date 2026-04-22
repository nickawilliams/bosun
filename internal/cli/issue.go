package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/config"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// issuePattern matches common issue tracker IDs like PROJ-123, CS-42, etc.
var issuePattern = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)

// addIssueFlag adds the shared --issue flag to a command.
func addIssueFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("issue", "i", "", "issue identifier (e.g. PROJ-123)")
}

// resolveIssue returns the issue identifier from the resolution chain:
// (1) --issue flag, (2) BOSUN_ISSUE env var, (3) workspace path derivation,
// (4) git branch name derivation.
func resolveIssue(cmd *cobra.Command) (string, error) {
	// (1) Check the flag.
	if issue, _ := cmd.Flags().GetString("issue"); issue != "" {
		return issue, nil
	}

	// (2) Check Viper (env var BOSUN_ISSUE via AutomaticEnv).
	if issue := viper.GetString("issue"); issue != "" {
		return issue, nil
	}

	// (3) Workspace path derivation.
	if issue := issueFromWorkspacePath(); issue != "" {
		return issue, nil
	}

	// (4) Git branch name derivation.
	if issue := issueFromBranch(); issue != "" {
		return issue, nil
	}

	// (5) Interactive issue picker (terminal only).
	if issue := pickOrPromptIssue(); issue != "" {
		return issue, nil
	}

	return "", fmt.Errorf(
		"issue not specified: use --issue, set BOSUN_ISSUE, or run from a workspace",
	)
}

// issueFromWorkspacePath attempts to extract an issue ID from the current
// working directory's position within a workspace.
func issueFromWorkspacePath() string {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return ""
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		return ""
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Try worktree-based detection (CWD is inside a repository worktree).
	if name, err := workspace.DetectName(wsRoot, cwd); err == nil {
		if issue := extractIssue(name); issue != "" {
			return issue
		}
	}

	// Fall back to the path relative to workspace root (CWD is the
	// workspace directory itself, not inside a worktree).
	if rel, err := filepath.Rel(wsRoot, cwd); err == nil && !strings.HasPrefix(rel, "..") {
		return extractIssue(rel)
	}

	return ""
}

// issueFromBranch attempts to extract an issue ID from the current git
// branch name.
func issueFromBranch() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	g := git.New()
	branch, err := g.GetCurrentBranch(context.Background(), cwd)
	if err != nil {
		return ""
	}

	return extractIssue(branch)
}

// manualEntry is the sentinel value used in the issue picker to
// indicate the user wants to type an issue key manually.
const manualEntry = "__manual__"

// pickOrPromptIssue tries to show an interactive picker of assigned
// issues. If the tracker is unavailable or the API call fails, falls
// back to a free-text prompt. Returns empty string in non-interactive
// mode.
func pickOrPromptIssue() string {
	if !isInteractive() {
		return ""
	}

	// Try the picker first.
	picked, err := pickAssignedIssue()
	if err != nil {
		return "" // User cancelled (ctrl+c).
	}
	if picked != "" && picked != manualEntry {
		return picked
	}

	// Manual entry fallback (chosen explicitly or picker unavailable).
	return promptRequired("Issue")
}

// pickAssignedIssue fetches assigned issues and presents a select
// picker. Returns the selected issue key, manualEntry if the user
// chose to enter manually, or empty string if the picker could not
// be shown. Returns ErrCancelled if the user pressed ctrl+c.
func pickAssignedIssue() (string, error) {
	tracker, err := newIssueTracker()
	if err != nil {
		return "", nil
	}

	slot := ui.NewSlot()

	var issues []issuepkg.Issue
	var columns []issuepkg.BoardColumn
	if err := slot.Run("Fetching assigned issues", func() error {
		var fetchErr error
		issues, fetchErr = tracker.ListIssues(context.Background(), issuepkg.ListQuery{
			AssignedToMe: true,
		})
		if fetchErr != nil {
			return fetchErr
		}
		boardID := viper.GetString("jira.board_id")
		if boardID != "" {
			// Best-effort: sort falls back to lifecycle keys on error.
			columns, _ = tracker.BoardColumns(context.Background(), boardID)
		}
		return nil
	}); err != nil {
		return "", nil
	}

	if len(issues) == 0 {
		slot.Clear()
		ui.Skip("No assigned issues found")
		return "", nil
	}

	sortIssues(issues, columns)

	// Build a status ID → column name map so the picker can show
	// the board column name instead of the raw Jira status name.
	colNames := buildColumnNameIndex(columns)

	// Compute column widths for aligned display.
	var maxKeyLen, maxTitleLen int
	for _, iss := range issues {
		if len(iss.Key) > maxKeyLen {
			maxKeyLen = len(iss.Key)
		}
		if len(iss.Title) > maxTitleLen {
			maxTitleLen = len(iss.Title)
		}
	}

	// Build select options.
	opts := make([]huh.Option[string], len(issues)+1)
	for i, iss := range issues {
		name := displayStatus(iss, colNames)
		label := fmt.Sprintf("%-*s  %-*s  %s", maxKeyLen, iss.Key, maxTitleLen, iss.Title, name)
		opts[i] = huh.NewOption(label, iss.Key)
	}
	opts[len(issues)] = huh.NewOption("Enter manually...", manualEntry)

	var selected string
	slot.Show(ui.NewCard(ui.CardInput, "Select Issue").Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Value(&selected),
	); err != nil {
		return "", err
	}
	slot.Clear()

	return selected, nil
}

// sortIssues sorts issues by board column order when columns are
// available, falling back to the hardcoded lifecycle status sequence.
// Issues with unknown statuses sort to the end. Within the same
// group, original order (from the API) is preserved via stable sort.
func sortIssues(issues []issuepkg.Issue, columns []issuepkg.BoardColumn) {
	if len(columns) > 0 {
		sortIssuesByBoard(issues, columns)
		return
	}
	sortIssuesByStatus(issues)
}

// sortIssuesByBoard sorts issues using the board column order.
// Statuses are mapped by ID to their column position (left to right).
func sortIssuesByBoard(issues []issuepkg.Issue, columns []issuepkg.BoardColumn) {
	idx := make(map[string]int)
	pos := 0
	for _, col := range columns {
		for _, id := range col.StatusIDs {
			idx[id] = pos
			pos++
		}
	}
	end := pos
	slices.SortStableFunc(issues, func(a, b issuepkg.Issue) int {
		ai, ok := idx[a.StatusID]
		if !ok {
			ai = end
		}
		bi, ok := idx[b.StatusID]
		if !ok {
			bi = end
		}
		return ai - bi
	})
}

// sortIssuesByStatus sorts issues by lifecycle status sequence.
// Used as a fallback when no board is configured.
func sortIssuesByStatus(issues []issuepkg.Issue) {
	idx := buildStatusIndex()
	if len(idx) == 0 {
		return
	}
	end := len(idx)
	slices.SortStableFunc(issues, func(a, b issuepkg.Issue) int {
		ai, ok := idx[strings.ToLower(a.Status)]
		if !ok {
			ai = end
		}
		bi, ok := idx[strings.ToLower(b.Status)]
		if !ok {
			bi = end
		}
		return ai - bi
	})
}

// skipBoard is the sentinel value used in the board picker to indicate
// the user wants to skip board selection.
const skipBoard = "__skip__"

// pickBoard fetches visible boards and presents a select picker.
// Returns the selected board ID, or empty string if the picker could
// not be shown or the user chose to skip.
func pickBoard() string {
	tracker, err := newIssueTracker()
	if err != nil {
		return ""
	}

	slot := ui.NewSlot()

	var boards []issuepkg.Board
	if err := slot.Run("Fetching boards", func() error {
		var fetchErr error
		boards, fetchErr = tracker.ListBoards(
			context.Background(),
			viper.GetString("jira.project"),
		)
		return fetchErr
	}); err != nil {
		return ""
	}

	if len(boards) == 0 {
		slot.Clear()
		ui.Skip("No boards found")
		return ""
	}

	opts := make([]huh.Option[string], len(boards)+1)
	for i, b := range boards {
		label := fmt.Sprintf("%s  (%s, id: %s)", b.Name, b.Type, b.ID)
		opts[i] = huh.NewOption(label, b.ID)
	}
	opts[len(boards)] = huh.NewOption("Skip", skipBoard)

	var selected string
	slot.Show(ui.NewCard(ui.CardInput, "Select Board").Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Value(&selected),
	); err != nil {
		return ""
	}
	slot.Clear()

	if selected == skipBoard {
		return ""
	}
	return selected
}

// buildColumnNameIndex returns a map from status ID to the board column
// name that contains it. Returns nil if columns is empty.
func buildColumnNameIndex(columns []issuepkg.BoardColumn) map[string]string {
	if len(columns) == 0 {
		return nil
	}
	idx := make(map[string]string)
	for _, col := range columns {
		for _, id := range col.StatusIDs {
			idx[id] = col.Name
		}
	}
	return idx
}

// displayStatus returns the board column name for an issue if available,
// otherwise falls back to the raw status name.
func displayStatus(iss issuepkg.Issue, colNames map[string]string) string {
	if name, ok := colNames[iss.StatusID]; ok {
		return name
	}
	return iss.Status
}

// extractIssue finds an issue tracker ID (e.g., PROJ-123) within a string.
// Works with branch names like "feature/PROJ-123_add-widget" or workspace
// paths like "feature/PROJ-123_add-widget".
func extractIssue(s string) string {
	return issuePattern.FindString(s)
}
