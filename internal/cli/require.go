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
			if ensureConfigValue(groupName, ck) {
				continue
			}
			if err := resolveConfigKey(groupName, ck, false); err != nil {
				return err
			}
			continue
		}

		// Unknown key — just check if it's set (config or BOSUN_* env).
		if viper.GetString(key) != "" {
			continue
		}
		if v := os.Getenv(envVarForKey(key)); v != "" {
			viper.Set(key, v)
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

// ensureConfigValue reports whether a schema key already has an
// effective value: viper (config file), or an env var — the key's
// explicit EnvVar or the automatic BOSUN_* name. Env-only values are
// materialized into viper so downstream bare viper.GetString reads
// (the provider factories) see exactly what this check saw. Bare
// viper misses both env forms for schema keys (no SetEnvKeyReplacer;
// explicit EnvVar names like GITHUB_TOKEN aren't bound at all), which
// used to make `config check` report a group green while the same
// group's requireConfig re-prompted for the token.
func ensureConfigValue(groupName string, ck ConfigKey) bool {
	fk := fullKey(groupName, ck)
	if viper.GetString(fk) != "" {
		return true
	}
	if v := effectiveEnvValue(groupName, ck); v != "" {
		viper.Set(fk, v)
		return true
	}
	return false
}

// resolveGroup ensures all required keys in a config group are populated.
// Keys that already have values are skipped (JIT mode for commands).
func resolveGroup(groupName string, group ConfigGroup) error {
	return resolveGroupMode(groupName, group, false, false)
}

// resolveGroupReconfigure prompts for all keys in a config group, using
// current values as defaults. Used by the init wizard so the user can
// review and change existing configuration.
func resolveGroupReconfigure(groupName string, group ConfigGroup) error {
	return resolveGroupMode(groupName, group, true, false)
}

// resolveGroupAsForm prompts the schema group's non-provider,
// non-secret keys as a single multi-field huh form, sets each entered
// value on viper, and returns the disk-bound values as a map for the
// caller to persist. Secret keys are skipped entirely — they live in
// env vars, and prompting for a value that would evaporate with the
// process would be a UX lie. The caller
// owns the actual file write so it can merge in the provider key
// the gate captured and emit confirmation cards at a single point.
// Returns a non-nil map even when empty so callers can unconditionally
// merge into it.
//
// formLabel is the CardInput heading ("Configure Jira" etc.); group
// keys named "provider" are skipped because the gate already captured
// the user's pick. Schema-driven Source fields aren't supported here
// (no current schema key uses Source); add a path when one does.
//
// Ctrl+c surfaces as ErrCancelled.
func resolveGroupAsForm(groupName, formLabel string, group ConfigGroup) (map[string]any, error) {
	if !isInteractive() {
		return map[string]any{}, nil
	}

	var keys []ConfigKey
	for _, ck := range group.Keys {
		if ck.Key == "provider" {
			continue
		}
		if ck.NoPrompt {
			// See ConfigKey.NoPrompt. The form prefills every field, so
			// a user tabbing through would persist the Example for a key
			// whose whole contract is that it stays unset.
			continue
		}
		if ck.Secret {
			// Secrets live in env vars, not the config file (no
			// keychain integration yet). Prompting here would be a
			// UX lie — the value would live only for the current
			// bosun-init process and disappear on exit, leaving the
			// next invocation to fail. The consolidated card tells
			// the user which env var to set; that's the durable
			// contract.
			continue
		}
		keys = append(keys, ck)
	}
	if len(keys) == 0 {
		return map[string]any{}, nil
	}

	// Pre-allocate so the per-field Value(&vals[i]) pointers stay
	// valid through the form's lifecycle — slice grow would
	// invalidate them.
	vals := make([]string, len(keys))
	fields := make([]huh.Field, 0, len(keys))

	for i, ck := range keys {
		// Default chain: current viper value (so reconfigure shows
		// existing values pre-filled, Tab through to keep), schema
		// default, then the example as a hint.
		current := viper.GetString(fullKey(groupName, ck))
		defaultVal := current
		if defaultVal == "" {
			defaultVal = ck.Default
		}
		if defaultVal == "" {
			defaultVal = ck.Example
		}
		vals[i] = defaultVal

		switch {
		case len(ck.Options) > 0:
			opts := make([]huh.Option[string], len(ck.Options))
			for j, o := range ck.Options {
				opts[j] = huh.NewOption(o, o)
			}
			fields = append(fields,
				newSelect[string](ck.Label).Options(opts...).Value(&vals[i]))
		default:
			fields = append(fields,
				newInput(ck.Label).Value(&vals[i]))
		}
	}

	rewind := ui.NewCard(ui.CardInput, formLabel).AccentBody().PrintRewindable()
	if err := runForm(fields...); err != nil {
		return nil, err
	}
	rewind()

	// Set viper for the session and collect disk-bound values into
	// kvs. Secrets were filtered out above, so every value here is
	// safe to persist.
	kvs := make(map[string]any)
	for i, ck := range keys {
		val := vals[i]
		if val == "" {
			continue
		}
		fk := fullKey(groupName, ck)
		viper.Set(fk, val)
		kvs[fk] = val
	}
	return kvs, nil
}

// resolveGroupMode is the shared implementation for group resolution.
// When forcePrompt is true, every key is prompted even if already set.
// When silent is true, per-key feedback cards (ui.Saved) are skipped —
// the disk + viper writes still happen unconditionally.
func resolveGroupMode(groupName string, group ConfigGroup, forcePrompt, silent bool) error {
	for _, ck := range group.Keys {
		fk := fullKey(groupName, ck)

		// Never offered, in any mode — see ConfigKey.NoPrompt. This
		// runs before the already-set check so the key is skipped
		// whether it holds a value or not: forcePrompt (the init
		// wizard's reconfigure pass) would otherwise prompt for it too.
		if ck.NoPrompt {
			continue
		}

		// Already set — config file, or an env var materialized into
		// viper by ensureConfigValue?
		if !forcePrompt && ensureConfigValue(groupName, ck) {
			continue
		}

		// Dynamic source — fetch options and show a picker.
		if ck.Source != nil && isInteractive() {
			picked, err := pickFromSource(ck)
			if err == nil && picked != "" {
				if err := saveConfigKeyMode(fk, ck.Label, picked, silent); err != nil {
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
		if err := resolveConfigKey(groupName, ck, silent); err != nil {
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

// saveConfigKeyMode persists the value to disk + viper unconditionally;
// the silent flag only gates the ui.Saved feedback card.
func saveConfigKeyMode(fk, label, val string, silent bool) error {
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
	if !silent {
		ui.Saved(label, val)
	}
	return nil
}

// resolveConfigKey prompts for a single config key using its schema metadata
// and saves the result to the appropriate config file (or env for secrets).
// When silent is true the per-key Saved confirmation card is suppressed;
// the prompt + disk write still happen unconditionally.
func resolveConfigKey(groupName string, ck ConfigKey, silent bool) error {
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
			rawInput().
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
		_ = os.Setenv(ck.EnvVar, val)
		viper.Set(fk, val)
		if !silent {
			ui.Saved(ck.Label, "(set for this session)")
		}
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

	// Defensive: any Secret key that reaches this point lacks an
	// EnvVar (the Secret + EnvVar case returned early above). Set
	// viper for the session but never write the value to disk —
	// putting a token into project YAML in cleartext is a footgun
	// we'd rather lose-the-value-on-exit than silently invite.
	if ck.Secret {
		viper.Set(fk, val)
		if !silent {
			ui.Saved(ck.Label, "(set for this session)")
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
	if !silent {
		ui.Saved(ck.Label, val)
	}

	return nil
}

// findConfigKey searches the schema for a fully-qualified key (e.g.,
// "issue_tracker.base_url") and returns the ConfigKey, its group name, and whether
// it was found.
func findConfigKey(key string) (ConfigKey, string, bool) {
	for groupName, group := range schemaGroups() {
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
