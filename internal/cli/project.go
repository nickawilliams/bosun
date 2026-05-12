package cli

import (
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// resolveProject returns the active project name from the resolution
// chain: (1) --project flag, (2) viper "project" (env BOSUN_PROJECT
// via AutomaticEnv or config file), (3) filepath.Base of the detected
// project root.
//
// Returns empty string when none of the above produce a value (i.e.,
// command is being run outside any bosun project).
func resolveProject(cmd *cobra.Command) string {
	if cmd != nil {
		if v, _ := cmd.Flags().GetString("project"); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(viper.GetString("project")); v != "" {
		return v
	}
	if root := config.FindProjectRoot(); root != "" {
		return filepath.Base(root)
	}
	return ""
}
