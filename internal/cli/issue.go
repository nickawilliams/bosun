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
	if picked := pickAssignedIssue(); picked != "" && picked != manualEntry {
		return picked
	}

	// Manual entry fallback (chosen explicitly or picker unavailable).
	return promptRequired("Issue")
}

// pickAssignedIssue fetches assigned issues and presents a select
// picker. Returns the selected issue key, manualEntry if the user
// chose to enter manually, or empty string if the picker could not
// be shown.
func pickAssignedIssue() string {
	tracker, err := newIssueTracker()
	if err != nil {
		return ""
	}

	slot := ui.NewSlot()

	var issues []issuepkg.Issue
	if err := slot.Run("Fetching assigned issues", func() error {
		var fetchErr error
		issues, fetchErr = tracker.ListIssues(context.Background(), issuepkg.ListQuery{
			AssignedToMe: true,
		})
		return fetchErr
	}); err != nil {
		return ""
	}

	if len(issues) == 0 {
		slot.Clear()
		ui.Skip("No assigned issues found")
		return ""
	}

	sortIssuesByStatus(issues)

	// Compute status column width for aligned display.
	maxStatusLen := 0
	for _, iss := range issues {
		if len(iss.Status) > maxStatusLen {
			maxStatusLen = len(iss.Status)
		}
	}

	// Build select options.
	opts := make([]huh.Option[string], len(issues)+1)
	for i, iss := range issues {
		label := fmt.Sprintf("%-*s  %s  %s", maxStatusLen, iss.Status, iss.Key, iss.Title)
		opts[i] = huh.NewOption(label, iss.Key)
	}
	opts[len(issues)] = huh.NewOption("Enter manually...", manualEntry)

	var selected string
	slot.Show(ui.NewCard(ui.CardInput, "Select issue").Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Value(&selected),
	); err != nil {
		return ""
	}
	slot.Clear()

	return selected
}

// sortIssuesByStatus sorts issues by lifecycle status sequence.
// Issues with unknown statuses sort to the end. Within the same
// status group, original order (updated DESC from the API) is
// preserved via stable sort.
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

// extractIssue finds an issue tracker ID (e.g., PROJ-123) within a string.
// Works with branch names like "feature/PROJ-123_add-widget" or workspace
// paths like "feature/PROJ-123_add-widget".
func extractIssue(s string) string {
	return issuePattern.FindString(s)
}
