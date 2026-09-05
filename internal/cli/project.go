package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
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

	// (2) Env. Read directly rather than through viper — see
	// resolveIssueSilent: a viper env layer would also match a
	// `project:` key in a config file, which is not where a
	// per-invocation override belongs.
	if v := strings.TrimSpace(os.Getenv("BOSUN_PROJECT")); v != "" {
		return v, nil
	}

	// (3) Auto-detect from CWD.
	if root := config.FindProjectRoot(); root != "" {
		return filepath.Base(root), nil
	}

	return "", nil
}
