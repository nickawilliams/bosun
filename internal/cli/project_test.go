package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newProjectTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	addProjectFlag(cmd)
	return cmd
}

func TestResolveProject(t *testing.T) {
	t.Run("from viper config", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "Configured")
		t.Cleanup(viper.Reset)

		cmd := newProjectTestCmd()
		got, err := resolveProject(cmd)
		if err != nil {
			t.Fatalf("resolveProject() error: %v", err)
		}
		if got != "Configured" {
			t.Errorf("got %q, want Configured", got)
		}
	})

	t.Run("trims viper whitespace", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "  Padded  ")
		t.Cleanup(viper.Reset)

		cmd := newProjectTestCmd()
		got, err := resolveProject(cmd)
		if err != nil {
			t.Fatalf("resolveProject() error: %v", err)
		}
		if got != "Padded" {
			t.Errorf("got %q, want Padded", got)
		}
	})

	t.Run("empty when nothing configured", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)

		cmd := newProjectTestCmd()
		// Outside a project root, resolveProject returns "".
		// In test environment FindProjectRoot may or may not find
		// a .bosun/ directory; we just verify no panic.
		_, _ = resolveProject(cmd)
	})

	t.Run("from flag", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)

		dir := t.TempDir()
		dir, _ = filepath.EvalSymlinks(dir)
		if err := os.Mkdir(filepath.Join(dir, ".bosun"), 0o755); err != nil {
			t.Fatal(err)
		}

		// Mirror PersistentPreRunE: validate + stash absolute path.
		config.ProjectRootOverride = dir
		t.Cleanup(func() { config.ProjectRootOverride = "" })

		cmd := newProjectTestCmd()
		cmd.SetArgs([]string{"--project", dir})
		_ = cmd.Execute()

		got, err := resolveProject(cmd)
		if err != nil {
			t.Fatalf("resolveProject() error: %v", err)
		}
		want := filepath.Base(dir)
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("flag takes precedence over env", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "FromEnv")
		t.Cleanup(viper.Reset)

		dir := t.TempDir()
		dir, _ = filepath.EvalSymlinks(dir)
		if err := os.Mkdir(filepath.Join(dir, ".bosun"), 0o755); err != nil {
			t.Fatal(err)
		}

		config.ProjectRootOverride = dir
		t.Cleanup(func() { config.ProjectRootOverride = "" })

		cmd := newProjectTestCmd()
		cmd.SetArgs([]string{"--project", dir})
		_ = cmd.Execute()

		got, err := resolveProject(cmd)
		if err != nil {
			t.Fatalf("resolveProject() error: %v", err)
		}
		want := filepath.Base(dir)
		if got != want {
			t.Errorf("got %q, want %q (flag should beat env)", got, want)
		}
	})
}
