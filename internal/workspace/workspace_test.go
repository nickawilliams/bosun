package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nickawilliams/bosun/internal/vcs/git"
)

// setupTestProject creates a project with repos and a workspace root.
// Returns (wsRoot, repos).
func setupTestProject(t *testing.T, repoNames ...string) (string, []Repo) {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)

	repoDir := filepath.Join(base, "repos")
	wsRoot := filepath.Join(base, "workspaces")
	os.MkdirAll(repoDir, 0o755)
	os.MkdirAll(wsRoot, 0o755)

	var repos []Repo
	for _, name := range repoNames {
		repoPath := filepath.Join(repoDir, name)
		steps := [][]string{
			{"git", "init", repoPath},
			{"git", "-C", repoPath, "config", "user.email", "test@test.com"},
			{"git", "-C", repoPath, "config", "user.name", "Test"},
			{"git", "-C", repoPath, "commit", "--allow-empty", "-m", "initial"},
		}
		for _, args := range steps {
			cmd := exec.Command(args[0], args[1:]...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v failed: %s\n%s", args, err, out)
			}
		}
		repos = append(repos, Repo{Name: name, Path: repoPath})
	}

	return wsRoot, repos
}

func TestCreateAndStatus(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api", "web")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	if err := mgr.Create(ctx, "test-branch", repos, true); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify workspace directory structure.
	for _, repo := range repos {
		wtPath := filepath.Join(wsRoot, "test-branch", repo.Name)
		if _, err := os.Stat(wtPath); err != nil {
			t.Errorf("worktree %s should exist", wtPath)
		}
	}

	// Check status.
	statuses, err := mgr.Status(ctx, "test-branch")
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("Status() returned %d repos, want 2", len(statuses))
	}
	for _, s := range statuses {
		if s.Branch != "test-branch" {
			t.Errorf("repo %s branch = %q, want %q", s.Name, s.Branch, "test-branch")
		}
		if s.Dirty {
			t.Errorf("repo %s should be clean", s.Name)
		}
	}
}

func TestCreateIdempotent(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	if err := mgr.Create(ctx, "idem-branch", repos, true); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Second call should not error.
	if err := mgr.Create(ctx, "idem-branch", repos, true); err != nil {
		t.Fatalf("Create() second call error: %v", err)
	}
}

func TestRemove(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "rm-branch", repos, true)

	if err := mgr.Remove(ctx, "rm-branch", repos, false); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	// Workspace directory should be gone.
	wsPath := filepath.Join(wsRoot, "rm-branch")
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Error("workspace directory should be removed")
	}
}

func TestRemoveBlockedByDirtyRepo(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "dirty-branch", repos, true)

	// Make the worktree dirty.
	wtPath := filepath.Join(wsRoot, "dirty-branch", "api")
	os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("x"), 0o644)

	// Should fail without force.
	if err := mgr.Remove(ctx, "dirty-branch", repos, false); err == nil {
		t.Error("Remove() should fail with dirty repo")
	}

	// Should succeed with force.
	if err := mgr.Remove(ctx, "dirty-branch", repos, true); err != nil {
		t.Fatalf("Remove(force=true) error: %v", err)
	}
}

func TestRemoveWithNestedBranchName(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-123", repos, true)

	if err := mgr.Remove(ctx, "feature/PROJ-123", repos, false); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	// Both the workspace dir and the empty parent (feature/) should be gone.
	if _, err := os.Stat(filepath.Join(wsRoot, "feature")); !os.IsNotExist(err) {
		t.Error("empty parent directory should be cleaned up")
	}
}

func TestMissingRepo(t *testing.T) {
	wsRoot, _ := setupTestProject(t)
	mgr := NewManager(git.New(), wsRoot)

	missing := []Repo{{Name: "nonexistent", Path: "/tmp/does-not-exist"}}
	err := mgr.Create(context.Background(), "test", missing, true)
	if err == nil {
		t.Error("Create() should fail for missing repo")
	}
}

func TestDetectName(t *testing.T) {
	wsRoot, repos := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-123", repos, true)

	wtPath := filepath.Join(wsRoot, "feature", "PROJ-123", "api")
	name, err := DetectName(wsRoot, wtPath)
	if err != nil {
		t.Fatalf("DetectName() error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-123") {
		t.Errorf("DetectName() = %q, want %q", name, "feature/PROJ-123")
	}
}
