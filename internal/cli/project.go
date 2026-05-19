package cli

import (
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/viper"
)

// resolveProject returns the active project's display name for the
// breadcrumb. Resolution: (1) viper "project" (env BOSUN_PROJECT or
// config file), (2) filepath.Base of the CWD-detected project root.
//
// A proper --project flag that accepts a project path and controls
// which project bosun operates on is deferred to a future branch —
// it requires changes to config loading, not just breadcrumb display.
//
// Returns empty string when not in a project context.
func resolveProject() string {
	if v := strings.TrimSpace(viper.GetString("project")); v != "" {
		return v
	}
	if root := config.FindProjectRoot(); root != "" {
		return filepath.Base(root)
	}
	return ""
}
