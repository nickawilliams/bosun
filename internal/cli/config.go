package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and manage bosun configuration",
	}

	cmd.AddCommand(
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigListCmd(),
		newConfigEditCmd(),
		newConfigPathCmd(),
	)

	return cmd
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			val := viper.Get(key)
			if val == nil {
				return fmt.Errorf("key %q not set", key)
			}
			fmt.Println(val)
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			value := args[1]
			global, _ := cmd.Flags().GetBool("global")

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			if err := setConfigValue(configPath, key, value); err != nil {
				return err
			}

			ui.Success("Set %s = %s", key, value)
			ui.Muted("  in %s", configPath)
			return nil
		},
	}

	cmd.Flags().BoolP("global", "g", false, "write to global config instead of project config")

	return cmd
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configuration values",
		RunE: func(cmd *cobra.Command, args []string) error {
			settings := viper.AllSettings()
			if len(settings) == 0 {
				ui.Muted("No configuration values set")
				return nil
			}

			flat := flattenMap("", settings)
			keys := make([]string, 0, len(flat))
			for k := range flat {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			for _, k := range keys {
				fmt.Printf("%s = %v\n", k, flat[k])
			}
			return nil
		},
	}
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

func newConfigPathCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Show configuration file paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			configDir, err := os.UserConfigDir()
			if err == nil {
				globalPath := filepath.Join(configDir, "bosun", "config.yaml")
				if _, err := os.Stat(globalPath); err == nil {
					ui.Item("Global:", globalPath)
				} else {
					ui.Item("Global:", globalPath+" (not found)")
				}
			}

			projectRoot := config.FindProjectRoot()
			if projectRoot != "" {
				projectPath := filepath.Join(projectRoot, ".bosun", "config.yaml")
				if _, err := os.Stat(projectPath); err == nil {
					ui.Item("Project:", projectPath)
				} else {
					ui.Item("Project:", projectPath+" (not found)")
				}
			} else {
				ui.Muted("  No project config (.bosun/ not found)")
			}

			return nil
		},
	}

	return cmd
}

// resolveConfigPath returns the path to the config file to write to.
func resolveConfigPath(global bool) (string, error) {
	if global {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("finding config directory: %w", err)
		}
		dir := filepath.Join(configDir, "bosun")
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

// setConfigValue does a targeted update of a single key in a YAML file,
// preserving comments and structure. For nested keys like "jira.base_url",
// it finds or creates the parent key and sets the child.
func setConfigValue(path, key, value string) error {
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading config: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	parts := strings.SplitN(key, ".", 2)

	if len(parts) == 1 {
		// Top-level key.
		lines = setYAMLKey(lines, key, value, 0)
	} else {
		// Nested key: find parent section, set child within it.
		parent := parts[0]
		child := parts[1]
		parentIdx := findYAMLKey(lines, parent, 0)
		if parentIdx == -1 {
			// Parent doesn't exist — append it.
			lines = append(lines, parent+":")
			lines = append(lines, fmt.Sprintf("  %s: %s", child, value))
		} else {
			// Find or set the child within the parent's indented block.
			lines = setYAMLKey(lines, child, value, parentIdx+1)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// findYAMLKey returns the line index of a key at the expected indentation
// level, or -1 if not found. startFrom specifies where to begin searching.
func findYAMLKey(lines []string, key string, startFrom int) int {
	prefix := key + ":"
	for i := startFrom; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, prefix) {
			return i
		}
	}
	return -1
}

// setYAMLKey finds a key starting from startFrom and updates its value,
// or appends it if not found.
func setYAMLKey(lines []string, key, value string, startFrom int) []string {
	idx := findYAMLKey(lines, key, startFrom)
	if idx != -1 {
		// Preserve existing indentation.
		indent := len(lines[idx]) - len(strings.TrimLeft(lines[idx], " "))
		lines[idx] = strings.Repeat(" ", indent) + key + ": " + value
	} else {
		// Determine indentation from context.
		indent := ""
		if startFrom > 0 && startFrom < len(lines) {
			// Match indentation of the section we're inserting into.
			for i := startFrom; i < len(lines); i++ {
				if strings.TrimSpace(lines[i]) != "" && !strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
					indent = strings.Repeat(" ", len(lines[i])-len(strings.TrimLeft(lines[i], " ")))
					break
				}
			}
			if indent == "" {
				indent = "  "
			}
		}
		// Insert after startFrom or append.
		newLine := indent + key + ": " + value
		if startFrom > 0 && startFrom <= len(lines) {
			lines = append(lines[:startFrom+1], append([]string{newLine}, lines[startFrom+1:]...)...)
		} else {
			lines = append(lines, newLine)
		}
	}
	return lines
}

// flattenMap recursively flattens a nested map into dot-separated keys.
func flattenMap(prefix string, m map[string]any) map[string]string {
	result := make(map[string]string)
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			for fk, fv := range flattenMap(key, val) {
				result[fk] = fv
			}
		default:
			result[key] = fmt.Sprintf("%v", val)
		}
	}
	return result
}
