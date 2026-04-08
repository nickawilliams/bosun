package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a bare-minimum git repo with one commit and
// origin/HEAD set. Returns the repo path.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)

	commands := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "initial"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v failed: %s\n%s", args, err, out)
		}
	}

	return dir
}

// initTestRepoWithRemote creates a repo with a bare remote and origin/HEAD.
func initTestRepoWithRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)

	bare := filepath.Join(base, "origin.git")
	repo := filepath.Join(base, "repo")

	steps := []struct {
		dir  string
		args []string
	}{
		{base, []string{"git", "init", "--bare", bare}},
		{base, []string{"git", "clone", bare, repo}},
		{repo, []string{"git", "config", "user.email", "test@test.com"}},
		{repo, []string{"git", "config", "user.name", "Test"}},
		{repo, []string{"git", "commit", "--allow-empty", "-m", "initial"}},
		{repo, []string{"git", "push", "origin", "main"}},
		{repo, []string{"git", "remote", "set-head", "origin", "--auto"}},
	}
	for _, s := range steps {
		cmd := exec.Command(s.args[0], s.args[1:]...)
		cmd.Dir = s.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v (in %s) failed: %s\n%s", s.args, s.dir, err, out)
		}
	}

	return repo
}

func TestCreateBranch(t *testing.T) {
	repo := initTestRepoWithRemote(t)
	a := New()
	ctx := context.Background()

	if err := a.CreateBranch(ctx, repo, "feature/test-123"); err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}

	exists, err := a.BranchExists(ctx, repo, "feature/test-123")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if !exists {
		t.Error("branch should exist after creation")
	}

	// Idempotent — second call should not error.
	if err := a.CreateBranch(ctx, repo, "feature/test-123"); err != nil {
		t.Fatalf("CreateBranch() second call error: %v", err)
	}
}

func TestCreateBranchFromHead(t *testing.T) {
	repo := initTestRepo(t)
	a := New()
	ctx := context.Background()

	if err := a.CreateBranchFromHead(ctx, repo, "test-branch"); err != nil {
		t.Fatalf("CreateBranchFromHead() error: %v", err)
	}

	exists, err := a.BranchExists(ctx, repo, "test-branch")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if !exists {
		t.Error("branch should exist after creation")
	}
}

func TestDeleteBranch(t *testing.T) {
	repo := initTestRepo(t)
	a := New()
	ctx := context.Background()

	a.CreateBranchFromHead(ctx, repo, "to-delete")
	if err := a.DeleteBranch(ctx, repo, "to-delete"); err != nil {
		t.Fatalf("DeleteBranch() error: %v", err)
	}

	exists, _ := a.BranchExists(ctx, repo, "to-delete")
	if exists {
		t.Error("branch should not exist after deletion")
	}

	// Idempotent — deleting non-existent branch should not error.
	if err := a.DeleteBranch(ctx, repo, "to-delete"); err != nil {
		t.Fatalf("DeleteBranch() second call error: %v", err)
	}
}

func TestGetCurrentBranch(t *testing.T) {
	repo := initTestRepo(t)
	a := New()

	branch, err := a.GetCurrentBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("GetCurrentBranch() error: %v", err)
	}
	// Default branch name for git init varies; just check it's non-empty.
	if branch == "" {
		t.Error("GetCurrentBranch() returned empty string")
	}
}

func TestGetDefaultBranch(t *testing.T) {
	repo := initTestRepoWithRemote(t)
	a := New()

	branch, err := a.GetDefaultBranch(context.Background(), repo)
	if err != nil {
		t.Fatalf("GetDefaultBranch() error: %v", err)
	}
	if branch != "main" {
		t.Errorf("GetDefaultBranch() = %q, want %q", branch, "main")
	}
}

func TestIsDirty(t *testing.T) {
	repo := initTestRepo(t)
	a := New()
	ctx := context.Background()

	dirty, err := a.IsDirty(ctx, repo)
	if err != nil {
		t.Fatalf("IsDirty() error: %v", err)
	}
	if dirty {
		t.Error("clean repo should not be dirty")
	}

	// Create an untracked file.
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644)

	dirty, err = a.IsDirty(ctx, repo)
	if err != nil {
		t.Fatalf("IsDirty() error: %v", err)
	}
	if !dirty {
		t.Error("repo with untracked file should be dirty")
	}
}

func TestWorktree(t *testing.T) {
	repo := initTestRepo(t)
	a := New()
	ctx := context.Background()

	a.CreateBranchFromHead(ctx, repo, "wt-branch")

	wtPath := filepath.Join(t.TempDir(), "worktree")
	if err := a.CreateWorktree(ctx, repo, wtPath, "wt-branch"); err != nil {
		t.Fatalf("CreateWorktree() error: %v", err)
	}

	// Verify worktree exists and is on the right branch.
	branch, err := a.GetCurrentBranch(ctx, wtPath)
	if err != nil {
		t.Fatalf("GetCurrentBranch() in worktree error: %v", err)
	}
	if branch != "wt-branch" {
		t.Errorf("worktree branch = %q, want %q", branch, "wt-branch")
	}

	if err := a.RemoveWorktree(ctx, repo, wtPath, false); err != nil {
		t.Fatalf("RemoveWorktree() error: %v", err)
	}
}
