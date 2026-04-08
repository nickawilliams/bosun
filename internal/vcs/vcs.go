package vcs

import "context"

// BranchStatus represents the state of a branch in a repository.
type BranchStatus struct {
	Name   string
	Exists bool
	Dirty  bool // Uncommitted changes present.
}

// VCS defines version control operations needed by bosun.
type VCS interface {
	// CreateBranch creates a new branch from the default branch in the
	// given repo. If the branch already exists, it returns nil.
	CreateBranch(ctx context.Context, repoPath, branchName string) error

	// CreateBranchFromHead creates a new branch from the current HEAD
	// in the given repo. If the branch already exists, it returns nil.
	CreateBranchFromHead(ctx context.Context, repoPath, branchName string) error

	// DeleteBranch removes a branch locally and from the remote.
	// Skips silently if the branch doesn't exist.
	DeleteBranch(ctx context.Context, repoPath, branchName string) error

	// GetBranchStatus returns the status of a branch in a repo.
	GetBranchStatus(ctx context.Context, repoPath, branchName string) (BranchStatus, error)

	// GetCurrentBranch returns the current branch name for a repo path.
	GetCurrentBranch(ctx context.Context, repoPath string) (string, error)

	// GetDefaultBranch returns the default branch (e.g., main, master)
	// for a repo by inspecting origin/HEAD.
	GetDefaultBranch(ctx context.Context, repoPath string) (string, error)

	// BranchExists returns true if the branch exists locally.
	BranchExists(ctx context.Context, repoPath, branchName string) (bool, error)

	// CreateWorktree creates a git worktree at worktreePath for the
	// given branch. The branch must already exist.
	CreateWorktree(ctx context.Context, repoPath, worktreePath, branchName string) error

	// RemoveWorktree removes a git worktree at the given path.
	RemoveWorktree(ctx context.Context, repoPath, worktreePath string, force bool) error

	// IsDirty returns true if the worktree at the given path has
	// uncommitted changes.
	IsDirty(ctx context.Context, path string) (bool, error)
}
