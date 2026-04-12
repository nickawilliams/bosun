package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// requireConfig ensures that the config key (or group) is populated. If
// the key matches a group in the schema, all required keys in that group
// are resolved. If it matches a single key, just that key is resolved.
// Missing values are prompted for interactively and saved. Callers read
// the resolved values from viper / os.Getenv afterward.
func requireConfig(keys ...string) error {
	for _, key := range keys {
		if group, ok := lookupGroup(key); ok {
			if err := resolveGroup(key, group); err != nil {
				return err
			}
			continue
		}

		if ck, groupName, ok := findConfigKey(key); ok {
			if ck.EnvVar != "" && os.Getenv(ck.EnvVar) != "" {
				continue
			}
			if viper.GetString(fullKey(groupName, ck)) != "" {
				continue
			}
			if err := resolveConfigKey(groupName, ck); err != nil {
				return err
			}
			continue
		}

		// Unknown key — just check if it's set.
		if viper.GetString(key) != "" {
			continue
		}
		if !isInteractive() {
			return fmt.Errorf("%s not configured", key)
		}
		val := promptValue(key, "")
		if val == "" {
			return fmt.Errorf("%s is required", key)
		}
		viper.Set(key, val)
	}

	return nil
}

// resolveGroup ensures all required keys in a config group are populated.
func resolveGroup(groupName string, group ConfigGroup) error {
	for _, ck := range group.Keys {
		fk := fullKey(groupName, ck)

		// Already set via env var?
		if ck.EnvVar != "" && os.Getenv(ck.EnvVar) != "" {
			continue
		}

		// Already set in viper?
		if viper.GetString(fk) != "" {
			continue
		}

		// Apply default for optional keys without prompting.
		if ck.Default != "" && !ck.Required {
			viper.Set(fk, ck.Default)
			continue
		}

		// Need to resolve (prompt or error).
		if err := resolveConfigKey(groupName, ck); err != nil {
			if ck.Required {
				return err
			}
			continue
		}
	}

	return nil
}

// resolveConfigKey prompts for a single config key using its schema metadata
// and saves the result to the appropriate config file (or env for secrets).
func resolveConfigKey(groupName string, ck ConfigKey) error {
	fk := fullKey(groupName, ck)

	if !isInteractive() {
		if ck.Default != "" {
			viper.Set(fk, ck.Default)
			return nil
		}
		if ck.EnvVar != "" {
			return fmt.Errorf("%s not set (set %s env var)", ck.Label, ck.EnvVar)
		}
		return fmt.Errorf("%s not configured (set %s in config)", ck.Label, fk)
	}

	// Secret env var — prompt with masked input, don't save to file.
	if ck.Secret && ck.EnvVar != "" {
		var val string
		rewind := ui.NewCard(ui.CardInput, ck.Label).PrintRewindable()
		if err := runForm(
			huh.NewInput().
				Placeholder("set for this session").
				EchoMode(huh.EchoModePassword).
				Value(&val),
		); err != nil {
			return err
		}
		rewind()
		if val == "" {
			return fmt.Errorf("%s is required", ck.Label)
		}
		os.Setenv(ck.EnvVar, val)
		ui.NewCard(ui.CardSuccess, ck.Label).
			Text("(set for this session)").
			Print()
		return nil
	}

	// Select from options or free-text input.
	var val string
	rewind := ui.NewCard(ui.CardInput, ck.Label).PrintRewindable()
	if len(ck.Options) > 0 {
		opts := make([]huh.Option[string], len(ck.Options))
		for i, o := range ck.Options {
			opts[i] = huh.NewOption(o, o)
		}
		if err := runForm(huh.NewSelect[string]().Options(opts...).Value(&val)); err != nil {
			return err
		}
	} else {
		defaultVal := ck.Default
		if defaultVal == "" {
			defaultVal = ck.Example
		}
		val = defaultVal
		if err := runForm(huh.NewInput().Value(&val)); err != nil {
			return err
		}
	}
	rewind()

	if val == "" {
		return fmt.Errorf("%s is required", ck.Label)
	}

	// Save to project config if inside a project, global otherwise.
	configPath, err := configPathForScope(false)
	if err != nil {
		viper.Set(fk, val)
		ui.NewCard(ui.CardSkipped, fmt.Sprintf("Could not save %s: %v", fk, err)).Print()
		return nil
	}

	if err := setConfigValue(configPath, fk, val); err != nil {
		viper.Set(fk, val)
		ui.NewCard(ui.CardSkipped, fmt.Sprintf("Could not save %s: %v", fk, err)).Print()
		return nil
	}

	viper.Set(fk, val)
	ui.NewCard(ui.CardSuccess, ck.Label).
		Text(val).
		Print()

	return nil
}

// findConfigKey searches the schema for a fully-qualified key (e.g.,
// "jira.base_url") and returns the ConfigKey, its group name, and whether
// it was found.
func findConfigKey(key string) (ConfigKey, string, bool) {
	for groupName, group := range configSchema {
		for _, ck := range group.Keys {
			if fullKey(groupName, ck) == key {
				return ck, groupName, true
			}
		}
	}
	return ConfigKey{}, "", false
}

// configPathForScope returns the config file path for the given scope.
func configPathForScope(global bool) (string, error) {
	if global {
		dir, err := config.GlobalConfigDir()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return filepath.Join(dir, "config.yaml"), nil
	}

	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return configPathForScope(true)
	}
	return filepath.Join(projectRoot, ".bosun", "config.yaml"), nil
}
