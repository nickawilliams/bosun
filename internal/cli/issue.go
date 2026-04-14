package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
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

	// (5) Interactive prompt (terminal only).
	if issue := promptRequired("Issue"); issue != "" {
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

	// Try worktree-based detection (CWD is inside a repo worktree).
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

// extractIssue finds an issue tracker ID (e.g., PROJ-123) within a string.
// Works with branch names like "feature/PROJ-123_add-widget" or workspace
// paths like "feature/PROJ-123_add-widget".
func extractIssue(s string) string {
	return issuePattern.FindString(s)
}
