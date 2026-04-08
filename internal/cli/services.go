package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/viper"
)

// newWorkspaceManager creates a workspace.Manager from current config.
func newWorkspaceManager() (*workspace.Manager, error) {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return nil, fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	repoRoot := viper.GetString("repository_root")
	if repoRoot == "" {
		repoRoot = filepath.Join(projectRoot, ".bosun", "repos")
	}
	if !filepath.IsAbs(repoRoot) {
		repoRoot = filepath.Join(projectRoot, repoRoot)
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		wsRoot = projectRoot
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	return workspace.NewManager(git.New(), repoRoot, wsRoot), nil
}

// repoRoot returns the resolved repository root path.
func repoRoot() (string, error) {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	root := viper.GetString("repository_root")
	if root == "" {
		root = filepath.Join(projectRoot, ".bosun", "repos")
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(projectRoot, root)
	}
	return root, nil
}

// resolveWorkspaceName returns the workspace name from args or auto-detects
// it from CWD.
func resolveWorkspaceName(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		wsRoot = projectRoot
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return workspace.DetectName(wsRoot, cwd)
}
