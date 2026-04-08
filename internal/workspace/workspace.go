package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/vcs"
)

// RepoStatus describes the state of a single repo within a workspace.
type RepoStatus struct {
	Name   string
	Branch string
	Dirty  bool
	Path   string
}

// Repo represents a resolved repository with a short name and absolute path.
type Repo struct {
	Name string // Directory basename, used for worktree directory names.
	Path string // Absolute path to the repo.
}

// Manager handles workspace lifecycle operations.
type Manager struct {
	vcs           vcs.VCS
	workspaceRoot string // Where workspaces are created.
}

// NewManager creates a workspace manager.
func NewManager(v vcs.VCS, workspaceRoot string) *Manager {
	return &Manager{
		vcs:           v,
		workspaceRoot: workspaceRoot,
	}
}

// Create creates a new workspace with worktrees for each repo.
// The branch name is the workspace name (can include slashes).
func (m *Manager) Create(ctx context.Context, name string, repos []Repo, fromHead bool) error {
	for _, repo := range repos {
		if _, err := os.Stat(repo.Path); err != nil {
			return fmt.Errorf("repo %q not found at %s", repo.Name, repo.Path)
		}

		worktreePath := filepath.Join(m.workspaceRoot, name, repo.Name)

		// Skip if worktree already exists.
		if _, err := os.Stat(worktreePath); err == nil {
			continue
		}

		// Create the branch if it doesn't exist.
		if fromHead {
			if err := m.vcs.CreateBranchFromHead(ctx, repo.Path, name); err != nil {
				return fmt.Errorf("creating branch in %s: %w", repo.Name, err)
			}
		} else {
			if err := m.vcs.CreateBranch(ctx, repo.Path, name); err != nil {
				return fmt.Errorf("creating branch in %s: %w", repo.Name, err)
			}
		}

		// Create the worktree directory (parent dirs).
		if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
			return fmt.Errorf("creating workspace directory: %w", err)
		}

		if err := m.vcs.CreateWorktree(ctx, repo.Path, worktreePath, name); err != nil {
			return fmt.Errorf("creating worktree for %s: %w", repo.Name, err)
		}
	}

	return nil
}

// Add adds repos to an existing workspace.
func (m *Manager) Add(ctx context.Context, name string, repos []Repo, fromHead bool) error {
	wsPath := filepath.Join(m.workspaceRoot, name)
	if _, err := os.Stat(wsPath); err != nil {
		return fmt.Errorf("workspace %q not found at %s", name, wsPath)
	}

	return m.Create(ctx, name, repos, fromHead)
}

// Status returns the status of all repos in a workspace.
func (m *Manager) Status(ctx context.Context, name string) ([]RepoStatus, error) {
	wsPath := filepath.Join(m.workspaceRoot, name)
	entries, err := os.ReadDir(wsPath)
	if err != nil {
		return nil, fmt.Errorf("reading workspace %q: %w", name, err)
	}

	var statuses []RepoStatus
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		repoPath := filepath.Join(wsPath, entry.Name())

		// Check if it looks like a worktree (has .git entry).
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
			continue
		}

		branch, err := m.vcs.GetCurrentBranch(ctx, repoPath)
		if err != nil {
			branch = "(unknown)"
		}

		dirty, err := m.vcs.IsDirty(ctx, repoPath)
		if err != nil {
			dirty = false
		}

		statuses = append(statuses, RepoStatus{
			Name:   entry.Name(),
			Branch: branch,
			Dirty:  dirty,
			Path:   repoPath,
		})
	}

	return statuses, nil
}

// Remove removes a workspace: worktrees, local branches, remote branches.
// repos maps repo names to their source paths (needed to run git worktree
// remove and branch delete against the source repo). Returns an error if any
// repo has uncommitted changes (unless force is true).
func (m *Manager) Remove(ctx context.Context, name string, repos []Repo, force bool) error {
	wsPath := filepath.Join(m.workspaceRoot, name)

	statuses, err := m.Status(ctx, name)
	if err != nil {
		return err
	}

	// Build a name→path lookup from the provided repos.
	repoPath := make(map[string]string, len(repos))
	for _, r := range repos {
		repoPath[r.Name] = r.Path
	}

	// Check for dirty repos unless force is set.
	if !force {
		var dirty []string
		for _, s := range statuses {
			if s.Dirty {
				dirty = append(dirty, s.Name)
			}
		}
		if len(dirty) > 0 {
			return fmt.Errorf(
				"repos have uncommitted changes: %s (use --force to override)",
				strings.Join(dirty, ", "),
			)
		}
	}

	// Remove worktrees and branches.
	for _, s := range statuses {
		srcPath, ok := repoPath[s.Name]
		if !ok {
			return fmt.Errorf("source repo path unknown for %q: provide it via repos config", s.Name)
		}
		worktreePath := filepath.Join(wsPath, s.Name)

		if err := m.vcs.RemoveWorktree(ctx, srcPath, worktreePath, force); err != nil {
			return fmt.Errorf("removing worktree for %s: %w", s.Name, err)
		}

		if err := m.vcs.DeleteBranch(ctx, srcPath, name); err != nil {
			return fmt.Errorf("deleting branch in %s: %w", s.Name, err)
		}
	}

	// Clean up workspace directory (and empty parent dirs).
	if err := os.RemoveAll(wsPath); err != nil {
		return fmt.Errorf("removing workspace directory: %w", err)
	}
	cleanEmptyParents(m.workspaceRoot, wsPath)

	return nil
}

// DetectName attempts to determine the workspace name from a path within
// a workspace. Walks up from the given path looking for the workspace root.
func DetectName(workspaceRoot, currentPath string) (string, error) {
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(currentPath)
	if err != nil {
		return "", err
	}

	// Walk up from currentPath until we find a directory that is a direct
	// child of workspaceRoot (accounting for nested branch names like
	// feature/PROJ-123).
	dir := absPath
	for {
		// Check if this directory contains a .git entry (worktree marker).
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			// This is a worktree — its parent structure above workspaceRoot
			// is the workspace name.
			parent := filepath.Dir(dir)
			if rel, err := filepath.Rel(absRoot, parent); err == nil && !strings.HasPrefix(rel, "..") {
				return rel, nil
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("not inside a workspace under %s", workspaceRoot)
}

// cleanEmptyParents removes empty directories between child and stopAt,
// walking upward. Stops at stopAt or the first non-empty directory.
func cleanEmptyParents(stopAt, child string) {
	dir := filepath.Dir(child)
	for dir != stopAt && dir != filepath.Dir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}
}
