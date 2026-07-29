package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/fsutil"
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

// List returns the names of all workspaces under the workspace root.
// A workspace is identified as a directory containing at least one
// subdirectory that looks like a git worktree (has a `.git` entry).
// Workspace names may contain slashes when nested under intermediate
// directories (e.g., "feature/EX-30434_foo"). Returns nil with no
// error when the workspace root doesn't exist or is empty.
func (m *Manager) List() ([]string, error) {
	if _, err := os.Stat(m.workspaceRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading workspace root: %w", err)
	}

	var workspaces []string
	walkWorkspaces(m.workspaceRoot, m.workspaceRoot, &workspaces)
	return workspaces, nil
}

// walkWorkspaces recursively descends `path`, looking for "workspace"
// directories — those containing at least one subdirectory with a
// `.git` entry (indicating a git worktree). When found, records the
// path relative to `root` and stops descending. Otherwise, recurses
// into subdirectories.
func walkWorkspaces(path, root string, workspaces *[]string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	hasWorktree := false
	var subdirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if _, err := os.Stat(filepath.Join(child, ".git")); err == nil {
			hasWorktree = true
			continue
		}
		subdirs = append(subdirs, child)
	}
	if hasWorktree {
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." {
			*workspaces = append(*workspaces, rel)
		}
		return
	}
	for _, sd := range subdirs {
		walkWorkspaces(sd, root, workspaces)
	}
}

// Remove removes a workspace: every repo's worktree + branch, then the
// workspace directory itself. repositories maps repo names to their
// source paths (needed to run git worktree remove and branch delete
// against the source repository). Refuses if any repository has
// uncommitted changes unless force is true.
func (m *Manager) Remove(ctx context.Context, name string, repositories []Repository, force bool) error {
	wsPath := filepath.Join(m.workspaceRoot, name)

	statuses, err := m.Status(ctx, name)
	if err != nil {
		return err
	}

	// Remove every repo currently in the workspace via the shared
	// per-repo cleanup. Passing the full status list as the target set
	// removes them all.
	targets := make([]string, len(statuses))
	for i, s := range statuses {
		targets[i] = s.Name
	}
	if err := m.RemoveRepositories(ctx, name, repositories, targets, force); err != nil {
		return err
	}

	// Clean up workspace directory (and empty parent dirs).
	if err := os.RemoveAll(wsPath); err != nil {
		return fmt.Errorf("removing workspace directory: %w", err)
	}
	cleanEmptyParents(m.workspaceRoot, wsPath)

	return nil
}

// RemoveRepositories removes the named repos from a workspace: each
// repo's worktree, local branch, and remote branch. The workspace
// directory itself stays. repositories supplies the source-path lookup
// (same shape as Remove). Refuses if any of the named repos is dirty
// or has commits not pushed to its remote tracking branch, unless
// force is true. Names that aren't currently in the workspace are
// silently skipped.
func (m *Manager) RemoveRepositories(ctx context.Context, name string, repositories []Repository, names []string, force bool) error {
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

	// Build the set of repo names to act on; intersect against repos
	// actually present in the workspace.
	targetSet := make(map[string]bool, len(names))
	for _, n := range names {
		targetSet[n] = true
	}
	var targets []RepositoryStatus
	for _, s := range statuses {
		if targetSet[s.Name] {
			targets = append(targets, s)
		}
	}

	// Check for dirty repositories among the targets unless force is set.
	if !force {
		var dirty []string
		for _, s := range targets {
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

		// Unpushed commits are as unrecoverable as a dirty tree once
		// the branch is force-deleted below. Only the remote-tracking
		// case is checked: without a remote counterpart, Ahead counts
		// against the default branch, which reads nonzero for every
		// squash-merged branch — a false positive on the everyday
		// flow. Sync errors also skip the check; this layer has no
		// host data to disambiguate, and `bosun cleanup` runs the
		// full readiness gate for the cases that need it.
		var unpushed []string
		for _, s := range targets {
			if s.Branch == "" || s.Branch == "(unknown)" {
				continue
			}
			sync, err := m.vcs.GetBranchSync(ctx, s.Path, s.Branch)
			if err == nil && sync.HasRemote && sync.Ahead > 0 {
				unpushed = append(unpushed, fmt.Sprintf("%s (%d commit(s))", s.Name, sync.Ahead))
			}
		}
		if len(unpushed) > 0 {
			return fmt.Errorf(
				"repositories have commits not pushed to their remote branch: %s (use --force to override)",
				strings.Join(unpushed, ", "),
			)
		}
	}

	// Remove worktrees and branches.
	for _, s := range targets {
		srcPath, ok := repositoryPath[s.Name]
		if !ok {
			return fmt.Errorf("source repository path unknown for %q: provide it via repositories config", s.Name)
		}
		worktreePath := filepath.Join(wsPath, s.Name)

		if err := m.vcs.RemoveWorktree(ctx, srcPath, worktreePath, force); err != nil {
			return fmt.Errorf("removing worktree for %s: %w", s.Name, err)
		}

		// Delete the branch the worktree was actually on, which may
		// differ from the workspace name if a user manually checked
		// out a different branch in this worktree. Skip when the
		// branch couldn't be determined to avoid a bogus delete.
		if s.Branch == "" || s.Branch == "(unknown)" {
			continue
		}
		if err := m.vcs.DeleteBranch(ctx, srcPath, s.Branch); err != nil {
			return fmt.Errorf("deleting branch in %s: %w", s.Name, err)
		}
	}

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
		if err != nil || fsutil.HasMeaningfulEntries(entries) {
			break
		}
		// Junk-only (or empty) parent: RemoveAll so a lingering
		// .DS_Store doesn't leave the directory behind.
		_ = os.RemoveAll(dir)
		dir = filepath.Dir(dir)
	}
}
