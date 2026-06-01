package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestCommandTitle(t *testing.T) {
	root := &cobra.Command{Use: "bosun"}
	config := &cobra.Command{
		Use:         "config",
		Annotations: map[string]string{headerAnnotationTitle: "Config"},
	}
	show := &cobra.Command{
		Use:         "show",
		Annotations: map[string]string{headerAnnotationTitle: "show"},
	}
	root.AddCommand(config)
	config.AddCommand(show)

	t.Run("leaf command with annotations", func(t *testing.T) {
		got := commandTitle(show, CommandContext{})
		if got != "Config › show" {
			t.Errorf("commandTitle(show, CommandContext{}) = %q, want %q", got, "Config › show")
		}
	})

	t.Run("mid-level command", func(t *testing.T) {
		got := commandTitle(config, CommandContext{})
		if got != "Config" {
			t.Errorf("commandTitle(config) = %q, want %q", got, "Config")
		}
	})

	t.Run("root command returns empty", func(t *testing.T) {
		got := commandTitle(root, CommandContext{})
		if got != "" {
			t.Errorf("commandTitle(root) = %q, want empty", got)
		}
	})

	t.Run("falls back to command name without annotation", func(t *testing.T) {
		plain := &cobra.Command{Use: "doctor"}
		root.AddCommand(plain)
		got := commandTitle(plain, CommandContext{})
		if got != "doctor" {
			t.Errorf("commandTitle(plain) = %q, want %q", got, "doctor")
		}
	})
}
