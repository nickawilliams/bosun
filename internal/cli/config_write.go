package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func newConfigSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		Annotations: map[string]string{
			headerAnnotationTitle: "set",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]
			global, _ := cmd.Flags().GetBool("global")

			rootCard(cmd, key).Print()

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			if err := setConfigValue(configPath, key, value); err != nil {
				return err
			}

			ui.Saved(fmt.Sprintf("set %s = %s", key, value), configPath)
			return nil
		},
	}

	cmd.Flags().BoolP("global", "g", false, "write to global config instead of project config")

	return cmd
}

func newConfigUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a configuration value",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "unset",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			global, _ := cmd.Flags().GetBool("global")

			rootCard(cmd, key).Print()

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			removed, err := unsetConfigValue(configPath, key)
			if err != nil {
				return err
			}

			if !removed {
				ui.Skip(fmt.Sprintf("%s not set in %s", key, configPath))
				return nil
			}

			ui.Saved(fmt.Sprintf("removed %s", key), configPath)
			return nil
		},
	}

	cmd.Flags().BoolP("global", "g", false, "remove from global config instead of project config")

	return cmd
}

func newConfigEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open configuration in $EDITOR",
		RunE: func(cmd *cobra.Command, args []string) error {
			global, _ := cmd.Flags().GetBool("global")

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}

			c := exec.Command(editor, configPath)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}

	cmd.Flags().BoolP("global", "g", false, "edit global config instead of project config")

	return cmd
}

// resolveConfigPath returns the path to the config file to write to.
func resolveConfigPath(global bool) (string, error) {
	if global {
		dir, err := config.GlobalConfigDir()
		if err != nil {
			return "", fmt.Errorf("finding config directory: %w", err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("creating config directory: %w", err)
		}
		return filepath.Join(dir, "config.yaml"), nil
	}

	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (use --global for global config)")
	}
	return filepath.Join(projectRoot, ".bosun", "config.yaml"), nil
}

// setConfigValue sets a key in a config file using a fresh viper instance
// scoped to that file only. Handles dot-separated keys at any depth.
func setConfigValue(path, key, value string) error {
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig() // ignore error — file may not exist yet

	v.Set(key, value)
	return v.WriteConfigAs(path)
}

// setConfigMap sets a map value at a key in a config file.
func setConfigMap(path, key string, values map[string]string) error {
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig()

	v.Set(key, values)
	return v.WriteConfigAs(path)
}

// setConfigListValue sets a list value at a key in a config file.
func setConfigListValue(path, key string, values []string) error {
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig()

	v.Set(key, values)
	return v.WriteConfigAs(path)
}

// unsetConfigValue removes a key from a config file. Returns true if
// the key was found and removed.
func unsetConfigValue(path, key string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading config: %w", err)
	}

	// Parse into a raw map so we can delete keys.
	var data map[string]any
	if err := yaml.Unmarshal(content, &data); err != nil {
		return false, fmt.Errorf("parsing config: %w", err)
	}

	// Walk dot-separated segments to the parent, then delete the leaf.
	segments := strings.Split(key, ".")
	parent := data
	for _, seg := range segments[:len(segments)-1] {
		child, ok := parent[seg]
		if !ok {
			return false, nil
		}
		childMap, ok := child.(map[string]any)
		if !ok {
			return false, nil
		}
		parent = childMap
	}

	leaf := segments[len(segments)-1]
	if _, ok := parent[leaf]; !ok {
		return false, nil
	}
	delete(parent, leaf)

	out, err := yaml.Marshal(data)
	if err != nil {
		return false, fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return false, fmt.Errorf("writing config: %w", err)
	}
	return true, nil
}
