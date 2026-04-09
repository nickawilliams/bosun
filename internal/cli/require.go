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

// requireConfig resolves a single config key by name. If the key belongs to
// a known group in the schema, its metadata (label, options, scope, etc.) is
// used automatically. Otherwise it falls back to a plain text prompt.
func requireConfig(key string) (string, error) {
	// Already set — return immediately.
	if val := viper.GetString(key); val != "" {
		return val, nil
	}

	// Look for this key in the schema.
	if ck, groupName, ok := findConfigKey(key); ok {
		return resolveConfigKey(groupName, ck)
	}

	// Unknown key — prompt with just the key name.
	if !isInteractive() {
		return "", fmt.Errorf("%s not configured", key)
	}
	val := promptValue(key, "")
	if val == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	viper.Set(key, val)
	return val, nil
}

// requireGroup resolves all required keys in a config group, prompting for
// any that are missing. Returns a map of key → value (using the short key
// names, not fully qualified).
func requireGroup(groupName string) (map[string]string, error) {
	group, ok := lookupGroup(groupName)
	if !ok {
		return nil, fmt.Errorf("unknown config group: %s", groupName)
	}

	result := make(map[string]string)

	for _, ck := range group.Keys {
		fk := fullKey(groupName, ck)

		// Try env var first for secret keys.
		if ck.EnvVar != "" {
			if val := os.Getenv(ck.EnvVar); val != "" {
				result[ck.Key] = val
				continue
			}
		}

		// Try viper.
		if val := viper.GetString(fk); val != "" {
			result[ck.Key] = val
			continue
		}

		// Use default if available and not required to prompt.
		if ck.Default != "" && !ck.Required {
			result[ck.Key] = ck.Default
			viper.Set(fk, ck.Default)
			continue
		}

		// Need to prompt.
		val, err := resolveConfigKey(groupName, ck)
		if err != nil {
			if ck.Required {
				return nil, err
			}
			continue
		}
		result[ck.Key] = val
	}

	return result, nil
}

// resolveConfigKey prompts for a single config key using its schema metadata.
func resolveConfigKey(groupName string, ck ConfigKey) (string, error) {
	fk := fullKey(groupName, ck)

	if !isInteractive() {
		if ck.Default != "" {
			viper.Set(fk, ck.Default)
			return ck.Default, nil
		}
		if ck.EnvVar != "" {
			return "", fmt.Errorf("%s not set (set %s env var)", ck.Label, ck.EnvVar)
		}
		return "", fmt.Errorf("%s not configured (set %s in config)", ck.Label, fk)
	}

	// Secret env var — prompt with masked input, don't save to file.
	if ck.Secret && ck.EnvVar != "" {
		val := promptSecret(fmt.Sprintf("%s (set %s to persist)", ck.Label, ck.EnvVar))
		if val == "" {
			return "", fmt.Errorf("%s is required", ck.Label)
		}
		os.Setenv(ck.EnvVar, val)
		ui.Complete(fmt.Sprintf("Set %s for this session", ck.EnvVar))
		ui.Muted("  Add to your shell profile to persist: export %s=...", ck.EnvVar)
		return val, nil
	}

	// Select from options.
	var val string
	if len(ck.Options) > 0 {
		opts := make([]huh.Option[string], len(ck.Options))
		for i, o := range ck.Options {
			opts[i] = huh.NewOption(o, o)
		}
		if err := runForm(huh.NewSelect[string]().Title(ck.Label).Options(opts...).Value(&val)); err != nil {
			return "", err
		}
	} else {
		defaultVal := ck.Default
		if defaultVal == "" {
			defaultVal = ck.Example
		}
		val = promptValue(ck.Label, defaultVal)
	}

	if val == "" {
		return "", fmt.Errorf("%s is required", ck.Label)
	}

	// Determine scope from the group in the schema.
	global := true
	if group, ok := lookupGroup(groupName); ok {
		global = group.Scope == ScopeGlobal
	}

	// Save to config file.
	configPath, err := configPathForScope(global)
	if err != nil {
		viper.Set(fk, val)
		ui.Skip(fmt.Sprintf("Could not save %s: %v", fk, err))
		return val, nil
	}

	if err := setConfigValue(configPath, fk, val); err != nil {
		viper.Set(fk, val)
		ui.Skip(fmt.Sprintf("Could not save %s: %v", fk, err))
		return val, nil
	}

	viper.Set(fk, val)
	ui.Complete(fmt.Sprintf("Saved %s", fk))

	return val, nil
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
		// Fall back to global if no project root.
		return configPathForScope(true)
	}
	return filepath.Join(projectRoot, ".bosun", "config.yaml"), nil
}
