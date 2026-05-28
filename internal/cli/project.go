package cli

import (
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// resolveProject returns the active project's display name.
// Resolution: (1) --project flag (derive name from path),
// (2) BOSUN_PROJECT env, (3) filepath.Base of the CWD-detected project root.
// Returns ("", nil) when not in a project context.
func resolveProject(cmd *cobra.Command) (string, error) {
	// (1) Flag: derive display name from the provided path.
	if f := cmd.Flags().Lookup("project"); f != nil && f.Changed {
		abs, err := filepath.Abs(f.Value.String())
		if err != nil {
			return "", err
		}
		return filepath.Base(abs), nil
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
