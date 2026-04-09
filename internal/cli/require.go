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

// requireOpt configures requireConfig behavior.
type requireOpt func(*requireOpts)

type requireOpts struct {
	defaultVal string
	global     bool
	options    []string // If set, render a select instead of text input.
}

// withDefault pre-fills the prompt with a default value.
func withDefault(val string) requireOpt {
	return func(o *requireOpts) { o.defaultVal = val }
}

// withGlobal saves the value to global config instead of project config.
func withGlobal() requireOpt {
	return func(o *requireOpts) { o.global = true }
}

// withOptions restricts the value to a known set and renders a select prompt.
func withOptions(opts ...string) requireOpt {
	return func(o *requireOpts) { o.options = opts }
}

// requireConfig returns the value for a config key. If the key is not set
// and stdin is interactive, prompts the user and saves the value to the
// config file. Returns an error only if the value is still empty.
func requireConfig(key, label string, opts ...requireOpt) (string, error) {
	// Already set — return immediately.
	if val := viper.GetString(key); val != "" {
		return val, nil
	}

	var o requireOpts
	for _, opt := range opts {
		opt(&o)
	}

	// Not interactive — can't prompt.
	if !isInteractive() {
		return "", fmt.Errorf("%s not configured (set %s in config)", label, key)
	}

	// Prompt — select if options provided, text input otherwise.
	var val string
	if len(o.options) > 0 {
		opts := make([]huh.Option[string], len(o.options))
		for i, opt := range o.options {
			opts[i] = huh.NewOption(opt, opt)
		}
		if err := runForm(huh.NewSelect[string]().Title(label).Options(opts...).Value(&val)); err != nil {
			return "", err
		}
	} else {
		val = promptValue(label, o.defaultVal)
	}
	if val == "" {
		return "", fmt.Errorf("%s is required", label)
	}

	// Save to config file.
	configPath, err := configPathForScope(o.global)
	if err != nil {
		// Can't save, but we have the value — use it for this session.
		viper.Set(key, val)
		ui.Skip(fmt.Sprintf("Could not save %s: %v", key, err))
		return val, nil
	}

	if err := setConfigValue(configPath, key, val); err != nil {
		viper.Set(key, val)
		ui.Skip(fmt.Sprintf("Could not save %s: %v", key, err))
		return val, nil
	}

	viper.Set(key, val)
	ui.Complete(fmt.Sprintf("Saved %s", key))

	return val, nil
}

// requireEnv returns the value of an environment variable. If not set and
// stdin is interactive, prompts the user with masked input and sets it for
// the current process. Does not write to config files.
func requireEnv(envVar, label string) (string, error) {
	// Already set — return immediately.
	if val := os.Getenv(envVar); val != "" {
		return val, nil
	}

	// Not interactive — can't prompt.
	if !isInteractive() {
		return "", fmt.Errorf("%s environment variable not set", envVar)
	}

	// Prompt with masked input.
	val := promptSecret(fmt.Sprintf("%s (set %s to persist)", label, envVar))
	if val == "" {
		return "", fmt.Errorf("%s is required", label)
	}

	// Set for current process only.
	os.Setenv(envVar, val)
	ui.Complete(fmt.Sprintf("Set %s for this session", envVar))
	ui.Muted("  Add to your shell profile to persist: export %s=...", envVar)

	return val, nil
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
