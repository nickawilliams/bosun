package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
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
			headerAnnotationTitle: "set value",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			rawValue := args[1]
			global, _ := cmd.Flags().GetBool("global")
			format, _ := cmd.Flags().GetString("format")

			value, err := parseConfigValue(rawValue, format)
			if err != nil {
				return err
			}

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			if err := setConfigValue(configPath, key, value); err != nil {
				return err
			}

			printConfigWriteConfirmation("Wrote", key, "to", configPath)
			return nil
		},
	}

	addProjectFlag(cmd)
	cmd.Flags().BoolP("global", "g", false, "write to global config instead of project config")
	cmd.Flags().StringP("format", "f", "raw", "interpret <value> as: raw, yaml, json")

	return cmd
}

// parseConfigValue interprets the positional <value> arg per the -f
// flag. raw keeps it as a string (current behavior, fine for scalars
// since viper stringifies on read). yaml / json parse it as a literal
// in that format so the caller can write a subtree in one shot —
// mirrors `get -f yaml|json`'s output shape on the input side.
func parseConfigValue(raw, format string) (any, error) {
	switch format {
	case "raw":
		return raw, nil
	case "yaml":
		var v any
		if err := yaml.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("parsing value as yaml: %w", err)
		}
		return v, nil
	case "json":
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, fmt.Errorf("parsing value as json: %w", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unknown format %q (valid: raw, yaml, json)", format)
	}
}

func newConfigUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a configuration value",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "unset value",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			global, _ := cmd.Flags().GetBool("global")

			configPath, err := resolveConfigPath(global)
			if err != nil {
				return err
			}

			removed, err := unsetConfigValue(configPath, key)
			if err != nil {
				return err
			}

			if !removed {
				ui.Skip(fmt.Sprintf("%s not set in %s", key, shortPath(configPath)))
				return nil
			}

			printConfigWriteConfirmation("Removed", key, "from", configPath)
			return nil
		},
	}

	addProjectFlag(cmd)
	cmd.Flags().BoolP("global", "g", false, "remove from global config instead of project config")

	return cmd
}

func newConfigEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit",
		Short: "Open configuration in $EDITOR",
		Annotations: map[string]string{
			headerAnnotationTitle: "edit file",
		},
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

			// Split EDITOR on whitespace so multi-word values work:
			// `mate -w`, `code --wait`, `vim -p`, etc. Go's exec.Command
			// treats its first argument as the literal binary name (no
			// shell splitting), so without this `EDITOR="mate -w"`
			// looks for a binary called "mate -w" — quoting the space
			// into the executable name. strings.Fields handles the
			// common cases; truly exotic EDITOR values (quoted paths
			// with spaces) would need shell parsing, which we punt on.
			parts := strings.Fields(editor)
			if len(parts) == 0 {
				parts = []string{"vi"} // whitespace-only $EDITOR
			}
			c := exec.Command(parts[0], append(parts[1:], configPath)...)
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if err := c.Run(); err != nil {
				return err
			}

			// Editor exited cleanly. Re-read the config so the in-
			// process viper reflects the saved edits (the load that
			// happened at Bootstrap is now stale), then run the check
			// so the user sees validation feedback without having to
			// type a second command. A YAML-parse failure here is
			// surfaced as the command's error — the file was saved
			// but bosun can't load it, which the user needs to know.
			// loadConfig, not bare config.Load: the Reset just wiped
			// the schema's env bindings and defaults along with the
			// stale file state, and the check below reads through them.
			viper.Reset()
			if err := loadConfig(); err != nil {
				return fmt.Errorf("re-reading config after edit: %w", err)
			}
			return runConfigCheck(nil)
		},
	}

	// --project, like every sibling config subcommand: edit resolves
	// which file to open from the project root, so pinning the project
	// has to be possible here too.
	addProjectFlag(cmd)
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

// writeConfigAtomic persists v to path via temp-file + rename so a
// crash mid-write can't truncate the config (viper's WriteConfigAs
// writes in place). The temp file lives in the target's directory so
// the rename stays atomic on one filesystem.
func writeConfigAtomic(v *viper.Viper, path string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	if err := v.WriteConfigAs(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// setConfigValue sets a key in a config file using a fresh viper instance
// scoped to that file only. Handles dot-separated keys at any depth.
// value is `any` so scalar strings, sub-maps, and slices all flow
// through unchanged — viper handles serialization on WriteConfigAs.
func setConfigValue(path, key string, value any) error {
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig() // ignore error — file may not exist yet

	v.Set(key, value)
	return writeConfigAtomic(v, path)
}

// printConfigWriteConfirmation renders the one-line "<verb> <key>
// <prep> <file>" confirmation card used by both set and unset.
// Connector words (verb, prep) recede in muted; the key (bold +
// Primary) and file (subtle) carry the visual weight so the reader's
// eye lands on the two pieces of information that matter — what was
// changed and where.
func printConfigWriteConfirmation(verb, key, prep, file string) {
	verbStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	fileStyle := lipgloss.NewStyle().Foreground(ui.Palette.Subtle)

	ui.SuccessLine(fmt.Sprintf("%s %s %s %s",
		verbStyle.Render(verb),
		ui.Keyword(key),
		verbStyle.Render(prep),
		fileStyle.Render(shortPath(file)),
	))
}

// setConfigValues writes multiple key-value pairs to a config file in
// a single read-modify-write cycle. Use when init's per-section flow
// has gathered several keys and wants to persist them atomically —
// avoids the N round-trips that calling setConfigValue per key would
// cost, and means a partial-fail leaves the file in either the old
// or new state, never half-way between.
func setConfigValues(path string, kvs map[string]any) error {
	if len(kvs) == 0 {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig() // ignore error — file may not exist yet
	for k, val := range kvs {
		v.Set(k, val)
	}
	return writeConfigAtomic(v, path)
}

// setConfigListValue sets a list value at a key in a config file.
func setConfigListValue(path, key string, values []string) error {
	v := viper.New()
	v.SetConfigFile(path)
	_ = v.ReadInConfig()

	v.Set(key, values)
	return writeConfigAtomic(v, path)
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return false, fmt.Errorf("writing config: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("writing config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return false, fmt.Errorf("writing config: %w", err)
	}
	return true, nil
}
