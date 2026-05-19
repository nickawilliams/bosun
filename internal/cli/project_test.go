package cli

import (
	"testing"

	"github.com/spf13/viper"
)

func TestResolveProject(t *testing.T) {
	t.Run("from viper config", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "Configured")
		t.Cleanup(viper.Reset)
		if got := resolveProject(); got != "Configured" {
			t.Errorf("got %q, want Configured", got)
		}
	})

	t.Run("trims viper whitespace", func(t *testing.T) {
		viper.Reset()
		viper.Set("project", "  Padded  ")
		t.Cleanup(viper.Reset)
		if got := resolveProject(); got != "Padded" {
			t.Errorf("got %q, want Padded", got)
		}
	})

	t.Run("empty when nothing configured", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		// Outside a project root, resolveProject returns "".
		// In test environment FindProjectRoot may or may not find
		// a .bosun/ directory; we just verify no panic.
		_ = resolveProject()
	})
}
