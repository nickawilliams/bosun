package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/nickawilliams/bosun/internal/vcs"
)

// Adapter implements vcs.VCS using the git CLI.
type Adapter struct{}

// New returns a new Git adapter.
func New() *Adapter {
	return &Adapter{}
}

func (a *Adapter) CreateBranch(ctx context.Context, repositoryPath, branchName string) error {
	exists, err := a.BranchExists(ctx, repositoryPath, branchName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	defaultBranch, err := a.GetDefaultBranch(ctx, repositoryPath)
	if err != nil {
		return fmt.Errorf("getting default branch: %w", err)
	}

	// Fetch latest before branching.
	_ = run(ctx, repositoryPath, "fetch", "origin", defaultBranch)

	if err := run(ctx, repositoryPath, "branch", branchName, "origin/"+defaultBranch); err != nil {
		return err
	}

	return run(ctx, repositoryPath, "push", "-u", "origin", branchName)
}

func (a *Adapter) CreateBranchFromHead(ctx context.Context, repositoryPath, branchName string) error {
	exists, err := a.BranchExists(ctx, repositoryPath, branchName)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return run(ctx, repositoryPath, "branch", branchName)
}

func (a *Adapter) DeleteBranch(ctx context.Context, repositoryPath, branchName string) error {
	// Delete local branch (ignore error if it doesn't exist).
	_ = run(ctx, repositoryPath, "branch", "-D", branchName)

	// Delete remote branch (ignore error if it doesn't exist).
	_ = run(ctx, repositoryPath, "push", "origin", "--delete", branchName)

	return nil
}

func (a *Adapter) GetBranchStatus(ctx context.Context, repositoryPath, branchName string) (vcs.BranchStatus, error) {
	exists, err := a.BranchExists(ctx, repositoryPath, branchName)
	if err != nil {
		return vcs.BranchStatus{}, err
	}

	status := vcs.BranchStatus{
		Name:   branchName,
		Exists: exists,
	}

	if exists {
		dirty, err := a.IsDirty(ctx, repositoryPath)
		if err != nil {
			return status, err
		}
		status.Dirty = dirty
	}

	return status, nil
}

func (a *Adapter) GetCurrentBranch(ctx context.Context, repositoryPath string) (string, error) {
	out, err := output(ctx, repositoryPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("getting current branch: %w", err)
	}
	return out, nil
}

func (a *Adapter) GetDefaultBranch(ctx context.Context, repositoryPath string) (string, error) {
	out, err := output(ctx, repositoryPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	if err != nil {
		return "", fmt.Errorf(
			"getting default branch: %w (is origin/HEAD set? run: git remote set-head origin --auto)",
			err,
		)
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

func (a *Adapter) BranchExists(ctx context.Context, repositoryPath, branchName string) (bool, error) {
	err := run(ctx, repositoryPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branchName)
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

func (a *Adapter) CreateWorktree(ctx context.Context, repositoryPath, worktreePath, branchName string) error {
	return run(ctx, repositoryPath, "worktree", "add", worktreePath, branchName)
}

func (a *Adapter) RemoveWorktree(ctx context.Context, repositoryPath, worktreePath string, force bool) error {
	args := []string{"worktree", "remove", worktreePath}
	if force {
		args = append(args, "--force")
	}
	return run(ctx, repositoryPath, args...)
}

func (a *Adapter) IsDirty(ctx context.Context, path string) (bool, error) {
	out, err := output(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("checking dirty state: %w", err)
	}
	return out != "", nil
}

func (a *Adapter) Push(ctx context.Context, repositoryPath, branchName string) error {
	return run(ctx, repositoryPath, "push", "-u", "origin", branchName)
}

func (a *Adapter) UnpushedCommits(ctx context.Context, repositoryPath, branchName string) (int, error) {
	out, err := output(ctx, repositoryPath, "rev-list", "--count", "origin/"+branchName+".."+branchName)
	if err != nil {
		// Remote tracking branch doesn't exist — branch was never pushed.
		return -1, nil
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0, fmt.Errorf("parsing commit count: %w", err)
	}
	return n, nil
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
