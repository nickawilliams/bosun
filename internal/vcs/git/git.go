package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/nickawilliams/bosun/internal/vcs"
)

// Adapter implements vcs.VCS using the git CLI.
type Adapter struct{}

// New returns a new Git adapter.
func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) CreateBranch(ctx context.Context, repoPath, branchName string) error {
	exists, err := a.BranchExists(ctx, repoPath, branchName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	defaultBranch, err := a.GetDefaultBranch(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("getting default branch: %w", err)
	}

	// Fetch latest before branching.
	_ = run(ctx, repoPath, "fetch", "origin", defaultBranch)

	return run(ctx, repoPath, "branch", branchName, "origin/"+defaultBranch)
}

func (a *Adapter) CreateBranchFromHead(ctx context.Context, repoPath, branchName string) error {
	exists, err := a.BranchExists(ctx, repoPath, branchName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return run(ctx, repoPath, "branch", branchName)
}

func (a *Adapter) DeleteBranch(ctx context.Context, repoPath, branchName string) error {
	// Delete local branch (ignore error if it doesn't exist).
	_ = run(ctx, repoPath, "branch", "-D", branchName)

	// Delete remote branch (ignore error if it doesn't exist).
	_ = run(ctx, repoPath, "push", "origin", "--delete", branchName)

	return nil
}

func (a *Adapter) GetBranchStatus(ctx context.Context, repoPath, branchName string) (vcs.BranchStatus, error) {
	exists, err := a.BranchExists(ctx, repoPath, branchName)
	if err != nil {
		return vcs.BranchStatus{}, err
	}

	status := vcs.BranchStatus{
		Name:   branchName,
		Exists: exists,
	}

	if exists {
		dirty, err := a.IsDirty(ctx, repoPath)
		if err != nil {
			return status, err
		}
		status.Dirty = dirty
	}

	return status, nil
}

func (a *Adapter) GetCurrentBranch(ctx context.Context, repoPath string) (string, error) {
	out, err := output(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return out, nil
}

func (a *Adapter) GetDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	out, err := output(ctx, repoPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	if err != nil {
		return "", fmt.Errorf(
			"getting default branch: %w (is origin/HEAD set? run: git remote set-head origin --auto)",
			err,
		)
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

func (a *Adapter) BranchExists(ctx context.Context, repoPath, branchName string) (bool, error) {
	err := run(ctx, repoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
	if err != nil {
		// Exit code 1 means the ref doesn't exist (not an error).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (a *Adapter) CreateWorktree(ctx context.Context, repoPath, worktreePath, branchName string) error {
	return run(ctx, repoPath, "worktree", "add", worktreePath, branchName)
}

func (a *Adapter) RemoveWorktree(ctx context.Context, repoPath, worktreePath string, force bool) error {
	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	return run(ctx, repoPath, args...)
}

func (a *Adapter) IsDirty(ctx context.Context, path string) (bool, error) {
	out, err := output(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("checking dirty state: %w", err)
	}
	return out != "", nil
}

// run executes a git command in the given directory.
func run(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// output executes a git command and returns its trimmed stdout.
func output(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
