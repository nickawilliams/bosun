package vcs

import (
	"context"
	"time"
)

// BranchStatus represents the state of a branch in a repository.
type BranchStatus struct {
	Name   string
	Exists bool
	Dirty  bool // Uncommitted changes present.
}

// BranchSync describes a branch's relationship to its remote tracking
// branch (and, when no remote exists, to the project's default
// branch). Used by the status command to render the per-repo branch
// row's state (in sync / ahead / behind / diverged / unpushed).
type BranchSync struct {
	// HasRemote is true when the branch has a remote tracking
	// counterpart (i.e., has been pushed at least once). When false,
	// Ahead reports commits relative to the default branch instead.
	HasRemote bool
	// Ahead is the count of commits on the branch that the remote
	// (or default branch, when HasRemote=false) doesn't have.
	Ahead int
	// Behind is the count of commits on the remote that the branch
	// doesn't have. Always 0 when HasRemote=false.
	Behind int
}

// VCS defines version control operations needed by bosun.
type VCS interface {
	// CreateBranch creates a new branch from the default branch in the
	// given repository. If the branch already exists, it returns nil.
	CreateBranch(ctx context.Context, repositoryPath, branchName string) error

	// CreateBranchFromHead creates a new branch from the current HEAD
	// in the given repository. If the branch already exists, it returns nil.
	CreateBranchFromHead(ctx context.Context, repositoryPath, branchName string) error

	// DeleteBranch removes a branch locally and from the remote.
	// Skips silently if the branch doesn't exist.
	DeleteBranch(ctx context.Context, repositoryPath, branchName string) error

	// GetBranchStatus returns the status of a branch in a repository.
	GetBranchStatus(ctx context.Context, repositoryPath, branchName string) (BranchStatus, error)

	// GetCurrentBranch returns the current branch name for a repository path.
	GetCurrentBranch(ctx context.Context, repositoryPath string) (string, error)

	// GetDefaultBranch returns the default branch (e.g., main, master)
	// for a repository by inspecting origin/HEAD.
	GetDefaultBranch(ctx context.Context, repositoryPath string) (string, error)

	// BranchExists returns true if the branch exists locally.
	BranchExists(ctx context.Context, repositoryPath, branchName string) (bool, error)

	// CreateWorktree creates a git worktree at worktreePath for the
	// given branch. The branch must already exist.
	CreateWorktree(ctx context.Context, repositoryPath, worktreePath, branchName string) error

	// RemoveWorktree removes a git worktree at the given path.
	RemoveWorktree(ctx context.Context, repositoryPath, worktreePath string, force bool) error

	// IsDirty returns true if the worktree at the given path has
	// uncommitted changes.
	IsDirty(ctx context.Context, path string) (bool, error)

	// Push pushes a local branch to the remote, setting up tracking.
	Push(ctx context.Context, repositoryPath, branchName string) error

	// UnpushedCommits returns the number of local commits on branchName
	// that have not been pushed to the remote. Returns -1 if the branch
	// has no remote counterpart (never been pushed).
	UnpushedCommits(ctx context.Context, repositoryPath, branchName string) (int, error)

	// GetBranchSync returns the ahead/behind state of branchName
	// relative to its remote tracking branch. When the branch has
	// no remote counterpart, returns HasRemote=false with Ahead set
	// to the count of commits ahead of the project's default branch.
	GetBranchSync(ctx context.Context, repositoryPath, branchName string) (BranchSync, error)

	// ChangedFiles returns the file paths changed between base and HEAD
	// (i.e. `git diff <base>...HEAD --name-only`). The base ref is the
	// caller's policy choice — preview uses `origin/<default-branch>`,
	// prerelease uses the previous release tag. Paths are relative to
	// the repository root. Returns nil when no files differ. Callers
	// that need a fresh remote base should call Fetch first.
	ChangedFiles(ctx context.Context, repositoryPath, base string) ([]string, error)

	// Fetch updates a remote ref. Best-effort sibling of ChangedFiles
	// for callers that diff against a remote-tracking branch and want
	// the latest before computing the merge-base. Returns an error from
	// the underlying git command, which callers typically swallow since
	// the diff itself is the load-bearing call.
	Fetch(ctx context.Context, repositoryPath, remote, ref string) error

	// LastCommitTime returns the commit timestamp of the most recent
	// commit on branchName. Returns the zero time and an error when
	// the branch doesn't exist or the lookup fails.
	LastCommitTime(ctx context.Context, repositoryPath, branchName string) (time.Time, error)

	// IsMergedInto reports whether branch's tip is an ancestor of base.
	// True when every commit on branch is reachable from base (e.g.
	// merged, fast-forwarded, rebased onto base). Used by the cleanup
	// safety check to confirm work is preserved in base before allowing
	// the branch to be deleted. base may be a local or remote-tracking
	// ref (e.g. "origin/main").
	IsMergedInto(ctx context.Context, repositoryPath, branch, base string) (bool, error)

	// HeadSHA returns the commit SHA at HEAD for the given repository
	// or worktree path. Used to compare a worktree's current commit
	// against a PR's head SHA when classifying cleanup safety.
	HeadSHA(ctx context.Context, repositoryPath string) (string, error)

	// TagsContaining returns every tag whose history reaches sha.
	// Wraps `git tag --contains <sha>` — a single local syscall, no
	// network. Used by prerelease to detect when a workspace's HEAD
	// is already in an existing release tag (so we can skip the
	// CreateRelease call and surface the containing release in the
	// result card). Empty slice means no tag contains the commit.
	TagsContaining(ctx context.Context, repositoryPath, sha string) ([]string, error)

	// FetchTags refreshes tag refs from remote (`git fetch <remote>
	// --tags --quiet`). Used before TagsContaining so tags pushed by
	// other users in the last few minutes are visible locally —
	// without this the contains check is only as fresh as the user's
	// last manual fetch.
	FetchTags(ctx context.Context, repositoryPath, remote string) error
}
