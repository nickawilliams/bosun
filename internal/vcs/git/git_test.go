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

	_ = a.CreateBranchFromHead(ctx, repository, "to-delete")
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
	_ = os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("x"), 0o644)

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
	files, err := a.ChangedFiles(ctx, repo, "origin/main")
	if err != nil {
		t.Fatalf("ChangedFiles() error: %v", err)
	}
	if files != nil {
		t.Errorf("ChangedFiles() on default branch = %v, want nil", files)
	}

	// Create a feature branch, add a file, commit.
	_ = run(ctx, repo, "checkout", "-b", "feature/test")
	_ = os.WriteFile(filepath.Join(repo, "new.txt"), []byte("hello"), 0o644)
	_ = run(ctx, repo, "add", "new.txt")
	_ = run(ctx, repo, "commit", "-m", "add new.txt")

	files, err = a.ChangedFiles(ctx, repo, "origin/main")
	if err != nil {
		t.Fatalf("ChangedFiles() on feature branch error: %v", err)
	}
	if len(files) != 1 || files[0] != "new.txt" {
		t.Errorf("ChangedFiles() = %v, want [new.txt]", files)
	}

	// Add a file in a subdirectory.
	_ = os.MkdirAll(filepath.Join(repo, "cmd", "api"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "cmd", "api", "main.go"), []byte("package main"), 0o644)
	_ = run(ctx, repo, "add", "cmd/api/main.go")
	_ = run(ctx, repo, "commit", "-m", "add cmd/api/main.go")

	files, err = a.ChangedFiles(ctx, repo, "origin/main")
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

	_ = a.CreateBranchFromHead(ctx, repository, "wt-branch")

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

func TestIsMergedInto(t *testing.T) {
	repository := initTestRepositoryWithRemote(t)
	a := New()
	ctx := context.Background()

	// Branch points at the same commit as main → trivially merged.
	if err := a.CreateBranchFromHead(ctx, repository, "trivial"); err != nil {
		t.Fatalf("CreateBranchFromHead() error: %v", err)
	}
	merged, err := a.IsMergedInto(ctx, repository, "trivial", "main")
	if err != nil {
		t.Fatalf("IsMergedInto(trivial, main) error: %v", err)
	}
	if !merged {
		t.Error("trivial branch (same commit as main) should report merged")
	}

	// Branch with new commits past main → not merged.
	if err := run(ctx, repository, "checkout", "-b", "feature/unmerged"); err != nil {
		t.Fatalf("checkout -b error: %v", err)
	}
	_ = os.WriteFile(filepath.Join(repository, "f.txt"), []byte("x"), 0o644)
	_ = run(ctx, repository, "add", "f.txt")
	_ = run(ctx, repository, "commit", "-m", "unmerged work")

	merged, err = a.IsMergedInto(ctx, repository, "feature/unmerged", "main")
	if err != nil {
		t.Fatalf("IsMergedInto(feature/unmerged, main) error: %v", err)
	}
	if merged {
		t.Error("feature/unmerged has commits past main; should not be merged")
	}

	// After merging the feature back into main, it should report merged.
	_ = run(ctx, repository, "checkout", "main")
	if err := run(ctx, repository, "merge", "--no-ff", "feature/unmerged", "-m", "merge"); err != nil {
		t.Fatalf("merge error: %v", err)
	}
	merged, err = a.IsMergedInto(ctx, repository, "feature/unmerged", "main")
	if err != nil {
		t.Fatalf("IsMergedInto(feature/unmerged, main) post-merge error: %v", err)
	}
	if !merged {
		t.Error("feature/unmerged after merge to main should report merged")
	}

	// Unknown ref → error (not a silent false).
	if _, err := a.IsMergedInto(ctx, repository, "does-not-exist", "main"); err == nil {
		t.Error("IsMergedInto on unknown ref should return an error")
	}
}

func TestHeadSHA(t *testing.T) {
	repository := initTestRepository(t)
	a := New()
	ctx := context.Background()

	sha, err := a.HeadSHA(ctx, repository)
	if err != nil {
		t.Fatalf("HeadSHA() error: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("HeadSHA() = %q, expected 40-char SHA", sha)
	}

	// Make a new commit and confirm HEAD moves.
	_ = os.WriteFile(filepath.Join(repository, "x.txt"), []byte("x"), 0o644)
	_ = run(ctx, repository, "add", "x.txt")
	_ = run(ctx, repository, "commit", "-m", "second")

	sha2, err := a.HeadSHA(ctx, repository)
	if err != nil {
		t.Fatalf("HeadSHA() after second commit error: %v", err)
	}
	if sha2 == sha {
		t.Error("HeadSHA() should change after a new commit")
	}
}

// TestCreateWorktree_StaleRegistration covers the case where a worktree
// directory was deleted on disk (e.g., rm -rf) without `git worktree
// remove`. Git's worktree admin metadata still references the path, so
// a fresh `worktree add` at the same path fails with "missing but
// already registered worktree" unless prune runs first. CreateWorktree
// prunes implicitly so this works transparently.
func TestCreateWorktree_StaleRegistration(t *testing.T) {
	repository := initTestRepository(t)
	a := New()
	ctx := context.Background()

	_ = a.CreateBranchFromHead(ctx, repository, "wt-branch")

	wtPath := filepath.Join(t.TempDir(), "worktree")
	if err := a.CreateWorktree(ctx, repository, wtPath, "wt-branch"); err != nil {
		t.Fatalf("first CreateWorktree() error: %v", err)
	}

	// Simulate manual deletion: blow away the directory without telling git.
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatalf("RemoveAll(%q) error: %v", wtPath, err)
	}

	// Without the implicit prune, this would fail with "missing but
	// already registered worktree". With prune, it should succeed.
	if err := a.CreateWorktree(ctx, repository, wtPath, "wt-branch"); err != nil {
		t.Fatalf("re-CreateWorktree() error after stale registration: %v", err)
	}

	branch, err := a.GetCurrentBranch(ctx, wtPath)
	if err != nil {
		t.Fatalf("GetCurrentBranch() in re-added worktree error: %v", err)
	}
	if branch != "wt-branch" {
		t.Errorf("re-added worktree branch = %q, want %q", branch, "wt-branch")
	}
}
