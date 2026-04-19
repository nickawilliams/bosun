package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/vcs"
)

// RepositoryStatus describes the state of a single repository within a workspace.
type RepositoryStatus struct {
	Name   string
	Branch string
	Dirty  bool
	Path   string
}

// Repository represents a resolved repository with a short name and absolute path.
type Repository struct {
	Name string // Directory basename, used for worktree directory names.
	Path string // Absolute path to the repository.
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

// Create creates a new workspace with worktrees for each repository.
// The branch name is the workspace name (can include slashes).
func (m *Manager) Create(ctx context.Context, name string, repositories []Repository, fromHead bool) error {
	for _, repository := range repositories {
		if _, err := os.Stat(repository.Path); err != nil {
			return fmt.Errorf("repository %q not found at %s", repository.Name, repository.Path)
		}

		worktreePath := filepath.Join(m.workspaceRoot, name, repository.Name)

		// Skip if worktree already exists.
		if _, err := os.Stat(worktreePath); err == nil {
			continue
		}

		// Create the branch if it doesn't exist.
		if fromHead {
			if err := m.vcs.CreateBranchFromHead(ctx, repository.Path, name); err != nil {
				return fmt.Errorf("creating branch in %s: %w", repository.Name, err)
			}
		} else {
			if err := m.vcs.CreateBranch(ctx, repository.Path, name); err != nil {
				return fmt.Errorf("creating branch in %s: %w", repository.Name, err)
			}
		}

		// Create the worktree directory (parent dirs).
		if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
			return fmt.Errorf("creating workspace directory: %w", err)
		}

		if err := m.vcs.CreateWorktree(ctx, repository.Path, worktreePath, name); err != nil {
			return fmt.Errorf("creating worktree for %s: %w", repository.Name, err)
		}
	}

	return nil
}

// Add adds repositories to an existing workspace.
func (m *Manager) Add(ctx context.Context, name string, repositories []Repository, fromHead bool) error {
	wsPath := filepath.Join(m.workspaceRoot, name)
	if _, err := os.Stat(wsPath); err != nil {
		return fmt.Errorf("workspace %q not found at %s", name, wsPath)
	}

	return m.Create(ctx, name, repositories, fromHead)
}

// Status returns the status of all repositories in a workspace.
func (m *Manager) Status(ctx context.Context, name string) ([]RepositoryStatus, error) {
	wsPath := filepath.Join(m.workspaceRoot, name)
	entries, err := os.ReadDir(wsPath)
	if err != nil {
		return nil, fmt.Errorf("reading workspace %q: %w", name, err)
	}

	var statuses []RepositoryStatus
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		repositoryPath := filepath.Join(wsPath, entry.Name())

		// Check if it looks like a worktree (has .git entry).
		if _, err := os.Stat(filepath.Join(repositoryPath, ".git")); err != nil {
			continue
		}

		branch, err := m.vcs.GetCurrentBranch(ctx, repositoryPath)
		if err != nil {
			branch = "(unknown)"
		}

		dirty, err := m.vcs.IsDirty(ctx, repositoryPath)
		if err != nil {
			dirty = false
		}

		statuses = append(statuses, RepositoryStatus{
			Name:   entry.Name(),
			Branch: branch,
			Dirty:  dirty,
			Path:   repositoryPath,
		})
	}

	return statuses, nil
}

// Remove removes a workspace: worktrees, local branches, remote branches.
// repositories maps repository names to their source paths (needed to run git
// worktree remove and branch delete against the source repository). Returns an
// error if any repository has uncommitted changes (unless force is true).
func (m *Manager) Remove(ctx context.Context, name string, repositories []Repository, force bool) error {
	wsPath := filepath.Join(m.workspaceRoot, name)

	statuses, err := m.Status(ctx, name)
	if err != nil {
		return err
	}

	// Build a name→path lookup from the provided repositories.
	repositoryPath := make(map[string]string, len(repositories))
	for _, r := range repositories {
		repositoryPath[r.Name] = r.Path
	}

	// Check for dirty repositories unless force is set.
	if !force {
		var dirty []string
		for _, s := range statuses {
			if s.Dirty {
				dirty = append(dirty, s.Name)
			}
		}
		if len(dirty) > 0 {
			return fmt.Errorf(
				"repositories have uncommitted changes: %s (use --force to override)",
				strings.Join(dirty, ", "),
			)
		}
	}

	// Remove worktrees and branches.
	for _, s := range statuses {
		srcPath, ok := repositoryPath[s.Name]
		if !ok {
			return fmt.Errorf("source repository path unknown for %q: provide it via repositories config", s.Name)
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

// DetectWorkspace determines the workspace name from a path at or below a
// workspace directory. It walks progressively longer prefixes of the relative
// path from the workspace root, returning the first that contains worktree
// subdirectories (directories with a .git entry).
func (m *Manager) DetectWorkspace(currentPath string) (string, error) {
	absRoot, err := filepath.Abs(m.workspaceRoot)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(currentPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("not inside a workspace under %s", absRoot)
	}

	parts := strings.Split(rel, string(filepath.Separator))
	for i := 1; i <= len(parts); i++ {
		candidate := filepath.Join(parts[:i]...)
		candidatePath := filepath.Join(absRoot, candidate)

		if hasWorktreeChildren(candidatePath) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("not inside a workspace under %s", absRoot)
}

// hasWorktreeChildren reports whether dir contains at least one subdirectory
// with a .git entry (i.e. a git worktree).
func hasWorktreeChildren(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), ".git")); err == nil {
				return true
			}
		}
	}
	return false
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
