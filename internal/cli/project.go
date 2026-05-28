package cli

import (
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// resolveProject returns the active project's display name.
// Resolution: (1) --project flag (display name from validated
// ProjectRootOverride), (2) BOSUN_PROJECT env, (3) filepath.Base of
// the CWD-detected project root.
// Returns ("", nil) when not in a project context.
func resolveProject(cmd *cobra.Command) (string, error) {
	// (1) Flag: PersistentPreRunE has already validated the path and
	// stored its absolute form on config.ProjectRootOverride.
	if f := cmd.Flags().Lookup("project"); f != nil && f.Changed {
		return filepath.Base(config.ProjectRootOverride), nil
	}

	// (2) Env / config: BOSUN_PROJECT via viper.
	if v := strings.TrimSpace(viper.GetString("project")); v != "" {
		return v, nil
	}

	// (3) Auto-detect from CWD.
	if root := config.FindProjectRoot(); root != "" {
		return filepath.Base(root), nil
	}

	return "", nil
}
