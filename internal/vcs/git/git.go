package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

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
	// Clear stale worktree registrations before adding. Git refuses to
	// add a worktree at a path it already has a registration for, even
	// when the on-disk directory was deleted manually (rm -rf without
	// `git worktree remove`). Prune only removes registrations whose
	// target paths don't exist on disk, so it's safe to run
	// unconditionally and leaves valid worktrees alone.
	if err := run(ctx, repositoryPath, "worktree", "prune"); err != nil {
		return fmt.Errorf("pruning stale worktrees: %w", err)
	}
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

// GetBranchSync returns the ahead/behind state of branchName relative
// to its remote tracking branch. When no remote counterpart exists
// (never pushed), reports HasRemote=false with Ahead = commits ahead
// of the project's default branch.
func (a *Adapter) GetBranchSync(ctx context.Context, repositoryPath, branchName string) (vcs.BranchSync, error) {
	// `git rev-list --left-right --count A...B` returns "leftCount\trightCount"
	// — commits unique to A (left) and unique to B (right).
	out, err := output(ctx, repositoryPath, "rev-list", "--left-right", "--count", "origin/"+branchName+"..."+branchName)
	if err == nil {
		left, right, ok := strings.Cut(strings.TrimSpace(out), "\t")
		if !ok {
			return vcs.BranchSync{}, fmt.Errorf("parsing rev-list count: unexpected format %q", out)
		}
		behind, perr := strconv.Atoi(left)
		if perr != nil {
			return vcs.BranchSync{}, fmt.Errorf("parsing behind count: %w", perr)
		}
		ahead, perr := strconv.Atoi(right)
		if perr != nil {
			return vcs.BranchSync{}, fmt.Errorf("parsing ahead count: %w", perr)
		}
		return vcs.BranchSync{HasRemote: true, Ahead: ahead, Behind: behind}, nil
	}

	// No remote tracking branch — count commits ahead of the default
	// branch instead. This is the "unpushed" case.
	defaultBranch, err := a.GetDefaultBranch(ctx, repositoryPath)
	if err != nil {
		return vcs.BranchSync{}, fmt.Errorf("getting default branch: %w", err)
	}
	out, err = output(ctx, repositoryPath, "rev-list", "--count", defaultBranch+".."+branchName)
	if err != nil {
		// Branch may not exist locally either — return zero-state.
		return vcs.BranchSync{}, nil
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return vcs.BranchSync{}, fmt.Errorf("parsing commit count: %w", err)
	}
	return vcs.BranchSync{HasRemote: false, Ahead: ahead}, nil
}

// LastCommitTime returns the commit timestamp of the most recent commit
// on branchName (committer date, %ct in Unix epoch seconds).
func (a *Adapter) LastCommitTime(ctx context.Context, repositoryPath, branchName string) (time.Time, error) {
	out, err := output(ctx, repositoryPath, "log", "-1", "--format=%ct", branchName)
	if err != nil {
		return time.Time{}, fmt.Errorf("getting last commit time for %s: %w", branchName, err)
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing commit timestamp %q: %w", out, err)
	}
	return time.Unix(secs, 0), nil
}

func (a *Adapter) ChangedFiles(ctx context.Context, repositoryPath, base string) ([]string, error) {
	out, err := output(ctx, repositoryPath, "diff", "--name-only", base+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("listing changed files: %w", err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func (a *Adapter) Fetch(ctx context.Context, repositoryPath, remote, ref string) error {
	return run(ctx, repositoryPath, "fetch", remote, ref)
}

// IsMergedInto reports whether branch is an ancestor of base.
// `git merge-base --is-ancestor` exits 0 when branch's tip is reachable
// from base, exit 1 when it isn't, anything else when the call itself
// failed (e.g. an unknown ref). The non-binary exit codes have to be
// surfaced as errors so callers don't mistake "couldn't tell" for
// "definitely not merged" — that mistake would BLOCK cleanup on a
// transient git failure.
func (a *Adapter) IsMergedInto(ctx context.Context, repositoryPath, branch, base string) (bool, error) {
	err := run(ctx, repositoryPath, "merge-base", "--is-ancestor", branch, base)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// HeadSHA returns the commit SHA at HEAD for the given path. Works
// for both bare repos and worktrees (rev-parse traverses the linked
// .git file).
func (a *Adapter) HeadSHA(ctx context.Context, repositoryPath string) (string, error) {
	return output(ctx, repositoryPath, "rev-parse", "HEAD")
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
