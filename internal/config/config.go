package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Load initializes configuration from global and project-level sources.
// Global config: ~/.config/bosun/config.yaml
// Project config: .bosun/config.yaml (discovered by walking up from CWD)
func Load() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Global config directory.
	configDir, err := os.UserConfigDir()
	if err == nil {
		viper.AddConfigPath(filepath.Join(configDir, "bosun"))
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

	// Environment variable binding.
	viper.SetEnvPrefix("BOSUN")
	viper.AutomaticEnv()

	return nil
}

// FindProjectRoot walks up from the current directory looking for a .bosun/
// directory. Returns the path containing .bosun/, or empty string if not found.
func FindProjectRoot() string {
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
