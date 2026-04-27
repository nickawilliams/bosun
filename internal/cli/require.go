package cli

import (
	"errors"
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
// the resolved values from viper afterward.
func requireConfig(keys ...string) error {
	for _, key := range keys {
		if group, ok := lookupGroup(key); ok {
			if err := resolveGroup(key, group); err != nil {
				return err
			}
			continue
		}

		if ck, groupName, ok := findConfigKey(key); ok {
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
		val, err := promptValue(key, "")
		if err != nil {
			return err
		}
		if val == "" {
			return fmt.Errorf("%s is required", key)
		}
		viper.Set(key, val)
	}

	return nil
}

// resolveGroup ensures all required keys in a config group are populated.
// Keys that already have values are skipped (JIT mode for commands).
func resolveGroup(groupName string, group ConfigGroup) error {
	return resolveGroupMode(groupName, group, false)
}

// resolveGroupReconfigure prompts for all keys in a config group, using
// current values as defaults. Used by the init wizard so the user can
// review and change existing configuration.
func resolveGroupReconfigure(groupName string, group ConfigGroup) error {
	return resolveGroupMode(groupName, group, true)
}

// resolveGroupMode is the shared implementation for group resolution.
// When forcePrompt is true, every key is prompted even if already set.
func resolveGroupMode(groupName string, group ConfigGroup, forcePrompt bool) error {
	for _, ck := range group.Keys {
		fk := fullKey(groupName, ck)

		// Already set (config file, env var via AutomaticEnv, etc.)?
		if !forcePrompt && viper.GetString(fk) != "" {
			continue
		}

		// Dynamic source — fetch options and show a picker.
		if ck.Source != nil && isInteractive() {
			picked, err := pickFromSource(ck)
			if err == nil && picked != "" {
				if err := saveConfigKey(fk, ck.Label, picked); err != nil {
					return err
				}
				continue
			}
			// Source failed or returned empty — fall through to standard
			// prompt for required keys, skip for optional.
			if !ck.Required {
				continue
			}
		}

		// Apply default for optional keys without prompting (JIT only).
		if !forcePrompt && ck.Default != "" && !ck.Required {
			viper.Set(fk, ck.Default)
			continue
		}

		// Need to resolve (prompt or error).
		if err := resolveConfigKey(groupName, ck); err != nil {
			if errors.Is(err, ErrCancelled) {
				return err
			}
			if ck.Required {
				return err
			}
			continue
		}
	}

	return nil
}

// saveConfigKey persists a resolved config value to the project config file.
func saveConfigKey(fk, label, val string) error {
	configPath, err := configPathForScope(false)
	if err != nil {
		viper.Set(fk, val)
		return nil
	}
	if err := setConfigValue(configPath, fk, val); err != nil {
		viper.Set(fk, val)
		return nil
	}
	viper.Set(fk, val)
	ui.Saved(label, val)
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
			return fmt.Errorf("%s not set (set %s in config or %s env var)", ck.Label, fk, ck.EnvVar)
		}
		return fmt.Errorf("%s not configured (set %s in config)", ck.Label, fk)
	}

	// Secret env var — prompt with masked input, don't save to file.
	if ck.Secret && ck.EnvVar != "" {
		var val string
		rewind := ui.NewCard(ui.CardInput, ck.Label).Tight().PrintRewindable()
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
		viper.Set(fk, val)
		ui.Saved(ck.Label, "(set for this session)")
		return nil
	}

	// Determine the default value: prefer current config, then schema
	// default, then example placeholder.
	current := viper.GetString(fk)
	defaultVal := current
	if defaultVal == "" {
		defaultVal = ck.Default
	}
	if defaultVal == "" {
		defaultVal = ck.Example
	}

	// Select from options or free-text input.
	var val string
	rewind := ui.NewCard(ui.CardInput, ck.Label).Tight().PrintRewindable()
	if len(ck.Options) > 0 {
		opts := make([]huh.Option[string], len(ck.Options))
		for i, o := range ck.Options {
			opts[i] = huh.NewOption(o, o)
		}
		val = current
		if err := runForm(huh.NewSelect[string]().Options(opts...).Value(&val)); err != nil {
			return err
		}
	} else {
		input, field := newDefaultInput(defaultVal)
		if err := runForm(input); err != nil {
			return err
		}
		val = field.Resolved()
	}
	rewind()

	if val == "" {
		if ck.Required {
			return fmt.Errorf("%s is required", ck.Label)
		}
		return nil
	}

	// Save to project config if inside a project, global otherwise.
	configPath, err := configPathForScope(false)
	if err != nil {
		viper.Set(fk, val)
		ui.Skip(fmt.Sprintf("could not save %s: %v", fk, err))
		return nil
	}

	if err := setConfigValue(configPath, fk, val); err != nil {
		viper.Set(fk, val)
		ui.Skip(fmt.Sprintf("could not save %s: %v", fk, err))
		return nil
	}

	viper.Set(fk, val)
	ui.Saved(ck.Label, val)

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

const manualEntrySource = "__manual__"

// pickFromSource fetches options from a ConfigKey's Source, presents a select
// picker with an "Enter manually..." fallback, and returns the selected value.
// Returns ("", err) on source failure and ("", nil) if the user chose manual
// entry or the source returned no options.
func pickFromSource(ck ConfigKey) (string, error) {
	slot := ui.NewSlot()

	var items []SourceOption
	if err := slot.Run("fetching "+ck.Label, func() error {
		var e error
		items, e = ck.Source()
		return e
	}); err != nil {
		slot.Clear()
		return "", err
	}

	if len(items) == 0 {
		slot.Clear()
		return "", nil
	}

	opts := make([]huh.Option[string], len(items)+1)
	for i, item := range items {
		opts[i] = huh.NewOption(item.Label, item.Value)
	}
	opts[len(items)] = huh.NewOption("Enter manually...", manualEntrySource)

	var selected string
	height := min(len(opts), maxSelectHeight)
	slot.Show(ui.NewCard(ui.CardInput, ck.Label).Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Height(height).
			Value(&selected),
	); err != nil {
		return "", err
	}
	slot.Clear()

	if selected == manualEntrySource {
		return "", nil
	}
	return selected, nil
}
