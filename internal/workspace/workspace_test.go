package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nickawilliams/bosun/internal/vcs/git"
)

// setupTestProject creates a project with repositories and a workspace root.
// Returns (wsRoot, repositories).
func setupTestProject(t *testing.T, repositoryNames ...string) (string, []Repository) {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)

	repositoryDir := filepath.Join(base, "repositories")
	wsRoot := filepath.Join(base, "workspaces")
	os.MkdirAll(repositoryDir, 0o755)
	os.MkdirAll(wsRoot, 0o755)

	var repositories []Repository
	for _, name := range repositoryNames {
		repositoryPath := filepath.Join(repositoryDir, name)
		steps := [][]string{
			{"git", "init", repositoryPath},
			{"git", "-C", repositoryPath, "config", "user.email", "test@test.com"},
			{"git", "-C", repositoryPath, "config", "user.name", "Test"},
			{"git", "-C", repositoryPath, "commit", "--allow-empty", "-m", "initial"},
		}
		for _, args := range steps {
			cmd := exec.Command(args[0], args[1:]...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v failed: %s\n%s", args, err, out)
			}
		}
		repositories = append(repositories, Repository{Name: name, Path: repositoryPath})
	}

	return wsRoot, repositories
}

func TestCreateAndStatus(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api", "web")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	if err := mgr.Create(ctx, "test-branch", repositories, true); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Verify workspace directory structure.
	for _, repository := range repositories {
		wtPath := filepath.Join(wsRoot, "test-branch", repository.Name)
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
		t.Fatalf("Status() returned %d repositories, want 2", len(statuses))
	}
	for _, s := range statuses {
		if s.Branch != "test-branch" {
			t.Errorf("repository %s branch = %q, want %q", s.Name, s.Branch, "test-branch")
		}
		if s.Dirty {
			t.Errorf("repository %s should be clean", s.Name)
		}
	}
}

func TestCreateIdempotent(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	if err := mgr.Create(ctx, "idem-branch", repositories, true); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// Second call should not error.
	if err := mgr.Create(ctx, "idem-branch", repositories, true); err != nil {
		t.Fatalf("Create() second call error: %v", err)
	}
}

func TestRemove(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "rm-branch", repositories, true)

	if err := mgr.Remove(ctx, "rm-branch", repositories, false); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	// Workspace directory should be gone.
	wsPath := filepath.Join(wsRoot, "rm-branch")
	if _, err := os.Stat(wsPath); !os.IsNotExist(err) {
		t.Error("workspace directory should be removed")
	}
}

func TestRemoveBlockedByDirtyRepository(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "dirty-branch", repositories, true)

	// Make the worktree dirty.
	wtPath := filepath.Join(wsRoot, "dirty-branch", "api")
	os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("x"), 0o644)

	// Should fail without force.
	if err := mgr.Remove(ctx, "dirty-branch", repositories, false); err == nil {
		t.Error("Remove() should fail with dirty repository")
	}

	// Should succeed with force.
	if err := mgr.Remove(ctx, "dirty-branch", repositories, true); err != nil {
		t.Fatalf("Remove(force=true) error: %v", err)
	}
}

func TestRemoveWithNestedBranchName(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-123", repositories, true)

	if err := mgr.Remove(ctx, "feature/PROJ-123", repositories, false); err != nil {
		t.Fatalf("Remove() error: %v", err)
	}

	// Both the workspace dir and the empty parent (feature/) should be gone.
	if _, err := os.Stat(filepath.Join(wsRoot, "feature")); !os.IsNotExist(err) {
		t.Error("empty parent directory should be cleaned up")
	}
}

func TestMissingRepository(t *testing.T) {
	wsRoot, _ := setupTestProject(t)
	mgr := NewManager(git.New(), wsRoot)

	missing := []Repository{{Name: "nonexistent", Path: "/tmp/does-not-exist"}}
	err := mgr.Create(context.Background(), "test", missing, true)
	if err == nil {
		t.Error("Create() should fail for missing repository")
	}
}

func TestDetectWorkspaceFromWorktree(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api", "web")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-123", repositories, true)

	wtPath := filepath.Join(wsRoot, "feature", "PROJ-123", "api")
	name, err := mgr.DetectWorkspace(wtPath)
	if err != nil {
		t.Fatalf("DetectWorkspace() error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-123") {
		t.Errorf("DetectWorkspace() = %q, want %q", name, "feature/PROJ-123")
	}
}

func TestDetectWorkspaceFromRoot(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-456", repositories, true)

	// At the workspace root itself (not inside a worktree).
	wsPath := filepath.Join(wsRoot, "feature", "PROJ-456")
	name, err := mgr.DetectWorkspace(wsPath)
	if err != nil {
		t.Fatalf("DetectWorkspace() from workspace root error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-456") {
		t.Errorf("DetectWorkspace() = %q, want %q", name, "feature/PROJ-456")
	}
}

func TestDetectWorkspaceFromSubdir(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-789", repositories, true)

	subdir := filepath.Join(wsRoot, "feature", "PROJ-789", "api", "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	name, err := mgr.DetectWorkspace(subdir)
	if err != nil {
		t.Fatalf("DetectWorkspace() from subdirectory error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-789") {
		t.Errorf("DetectWorkspace() = %q, want %q", name, "feature/PROJ-789")
	}
}

func TestDetectWorkspaceNotInside(t *testing.T) {
	wsRoot, _ := setupTestProject(t)
	mgr := NewManager(git.New(), wsRoot)

	_, err := mgr.DetectWorkspace("/tmp")
	if err == nil {
		t.Error("DetectWorkspace() should fail when not inside a workspace")
	}
}

func TestDetectName(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-123", repositories, true)

	wtPath := filepath.Join(wsRoot, "feature", "PROJ-123", "api")
	name, err := DetectName(wsRoot, wtPath)
	if err != nil {
		t.Fatalf("DetectName() error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-123") {
		t.Errorf("DetectName() = %q, want %q", name, "feature/PROJ-123")
	}
}

func TestDetectNameFromSubdirectory(t *testing.T) {
	wsRoot, repositories := setupTestProject(t, "api")
	mgr := NewManager(git.New(), wsRoot)
	ctx := context.Background()

	mgr.Create(ctx, "feature/PROJ-456", repositories, true)

	// Create a nested subdirectory inside the worktree.
	subdir := filepath.Join(wsRoot, "feature", "PROJ-456", "api", "src", "pkg")
	os.MkdirAll(subdir, 0o755)

	name, err := DetectName(wsRoot, subdir)
	if err != nil {
		t.Fatalf("DetectName() from subdirectory error: %v", err)
	}
	if name != filepath.Join("feature", "PROJ-456") {
		t.Errorf("DetectName() = %q, want %q", name, "feature/PROJ-456")
	}
}
