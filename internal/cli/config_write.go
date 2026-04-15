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

			ui.Saved(fmt.Sprintf("Set %s = %s", key, value), configPath)
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

			ui.Saved(fmt.Sprintf("Removed %s", key), configPath)
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
		lines = setYAMLKey(lines, key, value, 0)
	} else {
		parent := parts[0]
		child := parts[1]
		parentIdx := findYAMLKey(lines, parent, 0)
		if parentIdx == -1 {
			lines = append(lines, parent+":")
			lines = append(lines, fmt.Sprintf("  %s: %s", child, value))
		} else {
			lines = setYAMLKey(lines, child, value, parentIdx+1)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// unsetConfigValue removes a key from a YAML file, preserving
// structure. Returns true if the key was found and removed.
func unsetConfigValue(path, key string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading config: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	parts := strings.SplitN(key, ".", 2)

	var idx int
	if len(parts) == 1 {
		idx = findYAMLKey(lines, key, 0)
	} else {
		parentIdx := findYAMLKey(lines, parts[0], 0)
		if parentIdx == -1 {
			return false, nil
		}
		idx = findYAMLKey(lines, parts[1], parentIdx+1)
	}

	if idx == -1 {
		return false, nil
	}

	lines = append(lines[:idx], lines[idx+1:]...)

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return false, fmt.Errorf("writing config: %w", err)
	}
	return true, nil
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
		indent := len(lines[idx]) - len(strings.TrimLeft(lines[idx], " "))
		lines[idx] = strings.Repeat(" ", indent) + key + ": " + value
	} else {
		indent := ""
		if startFrom > 0 && startFrom < len(lines) {
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
		newLine := indent + key + ": " + value
		if startFrom > 0 && startFrom <= len(lines) {
			lines = append(lines[:startFrom+1], append([]string{newLine}, lines[startFrom+1:]...)...)
		} else {
			lines = append(lines, newLine)
		}
	}
	return lines
}
