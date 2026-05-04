package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepository creates a bare-minimum git repository with one commit
// and origin/HEAD set. Returns the repository path.
func initTestRepository(t *testing.T) string {
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

// initTestRepositoryWithRemote creates a repository with a bare remote and origin/HEAD.
func initTestRepositoryWithRemote(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)

	bare := filepath.Join(base, "origin.git")
	repository :=filepath.Join(base, "repository")

	steps := []struct {
		dir  string
		args []string
	}{
		{base, []string{"git", "init", "--bare", "--initial-branch=main", bare}},
		{base, []string{"git", "clone", bare, repository}},
		{repository, []string{"git", "config", "user.email", "test@test.com"}},
		{repository, []string{"git", "config", "user.name", "Test"}},
		{repository, []string{"git", "commit", "--allow-empty", "-m", "initial"}},
		{repository, []string{"git", "push", "origin", "main"}},
		{repository, []string{"git", "remote", "set-head", "origin", "--auto"}},
	}
	for _, s := range steps {
		cmd := exec.Command(s.args[0], s.args[1:]...)
		cmd.Dir = s.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v (in %s) failed: %s\n%s", s.args, s.dir, err, out)
		}
	}

	return repository
}

func TestCreateBranch(t *testing.T) {
	repository :=initTestRepositoryWithRemote(t)
	a := New()
	ctx := context.Background()

	if err := a.CreateBranch(ctx, repository, "feature/test-123"); err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}

	exists, err := a.BranchExists(ctx, repository, "feature/test-123")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if !exists {
		t.Error("branch should exist after creation")
	}

	// Idempotent — second call should not error.
	if err := a.CreateBranch(ctx, repository, "feature/test-123"); err != nil {
		t.Fatalf("CreateBranch() second call error: %v", err)
	}
}

func TestCreateBranchFromHead(t *testing.T) {
	repository :=initTestRepository(t)
	a := New()
	ctx := context.Background()

	if err := a.CreateBranchFromHead(ctx, repository, "test-branch"); err != nil {
		t.Fatalf("CreateBranchFromHead() error: %v", err)
	}

	exists, err := a.BranchExists(ctx, repository, "test-branch")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if !exists {
		t.Error("branch should exist after creation")
	}
}

func TestDeleteBranch(t *testing.T) {
	repository :=initTestRepository(t)
	a := New()
	ctx := context.Background()

	a.CreateBranchFromHead(ctx, repository, "to-delete")
	if err := a.DeleteBranch(ctx, repository, "to-delete"); err != nil {
		t.Fatalf("DeleteBranch() error: %v", err)
	}

	exists, _ := a.BranchExists(ctx, repository, "to-delete")
	if exists {
		t.Error("branch should not exist after deletion")
	}

	// Idempotent — deleting non-existent branch should not error.
	if err := a.DeleteBranch(ctx, repository, "to-delete"); err != nil {
		t.Fatalf("DeleteBranch() second call error: %v", err)
	}
}

func TestGetCurrentBranch(t *testing.T) {
	repository :=initTestRepository(t)
	a := New()

	branch, err := a.GetCurrentBranch(context.Background(), repository)
	if err != nil {
		t.Fatalf("GetCurrentBranch() error: %v", err)
	}
	// Default branch name for git init varies; just check it's non-empty.
	if branch == "" {
		t.Error("GetCurrentBranch() returned empty string")
	}
}

func TestGetDefaultBranch(t *testing.T) {
	repository :=initTestRepositoryWithRemote(t)
	a := New()

	branch, err := a.GetDefaultBranch(context.Background(), repository)
	if err != nil {
		t.Fatalf("GetDefaultBranch() error: %v", err)
	}
	if branch != "main" {
		t.Errorf("GetDefaultBranch() = %q, want %q", branch, "main")
	}
}

func TestIsDirty(t *testing.T) {
	repository :=initTestRepository(t)
	a := New()
	ctx := context.Background()

	dirty, err := a.IsDirty(ctx, repository)
	if err != nil {
		t.Fatalf("IsDirty() error: %v", err)
	}
	if dirty {
		t.Error("clean repository should not be dirty")
	}

	// Create an untracked file.
	os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("x"), 0o644)

	dirty, err = a.IsDirty(ctx, repository)
	if err != nil {
		t.Fatalf("IsDirty() error: %v", err)
	}
	if !dirty {
		t.Error("repository with untracked file should be dirty")
	}
}

func TestChangedFiles(t *testing.T) {
	repo := initTestRepositoryWithRemote(t)
	a := New()
	ctx := context.Background()

	// No changes on main — should return nil.
	files, err := a.ChangedFiles(ctx, repo)
	if err != nil {
		t.Fatalf("ChangedFiles() error: %v", err)
	}
	if files != nil {
		t.Errorf("ChangedFiles() on default branch = %v, want nil", files)
	}

	// Create a feature branch, add a file, commit.
	run(ctx, repo, "checkout", "-b", "feature/test")
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("hello"), 0o644)
	run(ctx, repo, "add", "new.txt")
	run(ctx, repo, "commit", "-m", "add new.txt")

	files, err = a.ChangedFiles(ctx, repo)
	if err != nil {
		t.Fatalf("ChangedFiles() on feature branch error: %v", err)
	}
	if len(files) != 1 || files[0] != "new.txt" {
		t.Errorf("ChangedFiles() = %v, want [new.txt]", files)
	}

	// Add a file in a subdirectory.
	os.MkdirAll(filepath.Join(repo, "cmd", "api"), 0o755)
	os.WriteFile(filepath.Join(repo, "cmd", "api", "main.go"), []byte("package main"), 0o644)
	run(ctx, repo, "add", "cmd/api/main.go")
	run(ctx, repo, "commit", "-m", "add cmd/api/main.go")

	files, err = a.ChangedFiles(ctx, repo)
	if err != nil {
		t.Fatalf("ChangedFiles() after second commit error: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("ChangedFiles() returned %d files, want 2: %v", len(files), files)
	}
}

func TestWorktree(t *testing.T) {
	repository :=initTestRepository(t)
	a := New()
	ctx := context.Background()

	a.CreateBranchFromHead(ctx, repository, "wt-branch")

	wtPath := filepath.Join(t.TempDir(), "worktree")
	if err := a.CreateWorktree(ctx, repository, wtPath, "wt-branch"); err != nil {
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

	if err := a.RemoveWorktree(ctx, repository, wtPath, false); err != nil {
		t.Fatalf("RemoveWorktree() error: %v", err)
	}
}
