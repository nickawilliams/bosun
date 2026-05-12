package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newProjectTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	// Mirror the persistent --project flag on root so tests can
	// drive resolveProject without constructing the full command tree.
	cmd.Flags().String("project", "", "project name (overrides CWD detection)")
	return cmd
}

func TestResolveProject(t *testing.T) {
	t.Run("from flag", func(t *testing.T) {
		viper.Reset()
		cmd := newProjectTestCmd()
		cmd.SetArgs([]string{"--project", "Acme"})
		_ = cmd.Execute()
		if got := resolveProject(cmd); got != "Acme" {
			t.Errorf("got %q, want Acme", got)
		}
	})

	t.Run("from viper config", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "Configured")
		t.Cleanup(viper.Reset)
		cmd := newProjectTestCmd()
		if got := resolveProject(cmd); got != "Configured" {
			t.Errorf("got %q, want Configured", got)
		}
	})

	t.Run("flag beats config", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "Configured")
		t.Cleanup(viper.Reset)
		cmd := newProjectTestCmd()
		cmd.SetArgs([]string{"--project", "Override"})
		_ = cmd.Execute()
		if got := resolveProject(cmd); got != "Override" {
			t.Errorf("got %q, want Override", got)
		}
	})

	t.Run("trims viper whitespace", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "  Padded  ")
		t.Cleanup(viper.Reset)
		cmd := newProjectTestCmd()
		if got := resolveProject(cmd); got != "Padded" {
			t.Errorf("got %q, want Padded", got)
		}
	})

	t.Run("nil cmd falls back to viper", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "FromViper")
		t.Cleanup(viper.Reset)
		if got := resolveProject(nil); got != "FromViper" {
			t.Errorf("got %q, want FromViper", got)
		}
	})
}
