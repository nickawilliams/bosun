package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// GlobalConfigDir returns the bosun global config directory.
// Uses $XDG_CONFIG_HOME/bosun if set, otherwise ~/.config/bosun.
func GlobalConfigDir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "bosun"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "bosun"), nil
}

// Load initializes configuration from global and project-level sources.
// Global config: $XDG_CONFIG_HOME/bosun/config.yaml (or ~/.config/bosun/)
// Project config: .bosun/config.yaml (discovered by walking up from CWD)
func Load() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Global config directory.
	configDir, err := GlobalConfigDir()
	if err == nil {
		viper.AddConfigPath(configDir)
	}

	// Read global config (not an error if missing).
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("reading global config: %w", err)
		}
	}

	// Discover and merge project config.
	if projectDir := FindProjectRoot(); projectDir != "" {
		projectConfig := filepath.Join(projectDir, ".bosun", "config.yaml")
		if _, err := os.Stat(projectConfig); err == nil {
			v := viper.New()
			v.SetConfigFile(projectConfig)
			if err := v.ReadInConfig(); err != nil {
				return fmt.Errorf("reading project config: %w", err)
			}
			if err := viper.MergeConfigMap(v.AllSettings()); err != nil {
				return fmt.Errorf("merging project config: %w", err)
			}
		}
	}

	// No AutomaticEnv, deliberately. bosun's env vars name a key as
	// BOSUN_ + the key path uppercased with dots turned into
	// underscores (BOSUN_ISSUE_TRACKER_TOKEN → issue_tracker.token),
	// and AutomaticEnv cannot produce that mapping: it joins the key
	// path with viper's delimiter and only uppercases, so it looks for
	// BOSUN_ISSUE_TRACKER.TOKEN — a name no shell exports. What it did
	// do was shadow dotted keys: viper treats an env var matching a
	// key path's FIRST segment as covering everything beneath it and
	// returns nil for the nested read, which is how exporting
	// BOSUN_WORKSPACE emptied workspace.root and broke every command
	// reading it (#110). The context vars (BOSUN_ISSUE, BOSUN_PROJECT,
	// BOSUN_WORKSPACE) are per-invocation overrides, not config, and
	// are read with os.Getenv directly.
	//
	// The env layer that IS on is explicit per-key registration: the
	// cli layer calls viper.BindEnv for every schema key right after
	// this load (see cli's bindSchemaEnv), mapping the BOSUN_* names —
	// and each key's explicit EnvVar like GITHUB_TOKEN — through viper
	// itself, so a bare viper.Get* resolves env → project → global →
	// default. Registration can't live here: the schema belongs to the
	// cli layer, which splices in provider-contributed keys this
	// package must not know about. BindEnv cannot shadow a block (a
	// binding names one exact key), and it does not compose with
	// AutomaticEnv — viper's shadow check runs before it consults the
	// bound names — so AutomaticEnv stays off either way.

	return nil
}

// ProjectRootOverride, when non-empty, bypasses CWD-based discovery
// and is returned directly by FindProjectRoot. Set by the CLI layer
// when the --project flag is provided.
var ProjectRootOverride string

// FindProjectRoot walks up from the current directory looking for a .bosun/
// directory. Returns the path containing .bosun/, or empty string if not found.
// When ProjectRootOverride is set, returns that value directly.
func FindProjectRoot() string {
	if ProjectRootOverride != "" {
		return ProjectRootOverride
	}

	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, ".bosun")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// RepoConfigFile is the name of a repository's own bosun descriptor,
// committed to that repository and read as the innermost config layer.
//
// A file rather than a `.bosun/` directory, and the distinction is
// load-bearing: FindProjectRoot walks up looking for `.bosun/` and
// returns the first hit, so a repository carrying its config in a
// directory of that name would shadow the workspace's project root for
// every command run from inside it. A distinct filename leaves the walk
// untouched.
const RepoConfigFile = ".bosun.yaml"

// LoadRepoConfig reads the descriptor at <repoPath>/.bosun.yaml into
// its own viper instance. It returns (nil, nil) when the repository has
// no descriptor, which is the ordinary case — every repository must
// keep working without one, resolving repo-scoped keys from the central
// layers instead.
//
// The result is deliberately NOT merged into the global viper: there is
// one descriptor per repository and a single command reads several of
// them in one fan-out, so merging would let the last repository read
// win for all of them. The CLI layer overlays it per repository.
func LoadRepoConfig(repoPath string) (*viper.Viper, error) {
	if repoPath == "" {
		return nil, nil
	}

	path := filepath.Join(repoPath, RepoConfigFile)
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}

	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return v, nil
}
