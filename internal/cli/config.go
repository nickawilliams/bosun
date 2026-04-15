package cli

import (
	"fmt"
	"image/color"
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)


// Source-encoded glyphs and colors for the config tree.
const (
	glyphDefault = "◻︎"
	glyphGlobal  = "◼︎"
	glyphProject = "◆"
	glyphEnv     = "▲"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and manage bosun configuration",
		// Bare "config" runs "show".
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigShow(cmd, args)
		},
	}

	showCmd := newConfigShowCmd()

	cmd.AddCommand(
		showCmd,
		newConfigCheckCmd(),
		newConfigGetCmd(),
		newConfigSetCmd(),
		newConfigUnsetCmd(),
		newConfigEditCmd(),
	)

	// Inherit show's flags on the parent so "config --source env" works.
	cmd.Flags().StringP("output", "o", "", "output format: yaml, json, env")
	cmd.Flags().StringSlice("source", nil, "filter by source: global, project, env, default (repeatable)")

	return cmd
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [group]",
		Short: "Display effective resolved configuration",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "show",
		},
		RunE: runConfigShow,
	}

	cmd.Flags().StringP("output", "o", "", "output format: yaml, json, env")
	cmd.Flags().StringSlice("source", nil, "filter by source: global, project, env, default (repeatable)")

	return cmd
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	outputFmt, _ := cmd.Flags().GetString("output")
	sourceFilter, _ := cmd.Flags().GetStringSlice("source")
	var groupFilter string
	if len(args) > 0 {
		groupFilter = args[0]
	}

	// Machine-readable output.
	if outputFmt != "" {
		return runConfigShowMachine(outputFmt, sourceFilter, groupFilter)
	}

	// Human-readable tree display.
	cs := loadConfigSources()

	rootCard(cmd).Tight().Print()
	tree := buildConfigTree(cs, sourceFilter, groupFilter)
	tree.Print()

	// Sources hint line at the end.
	fmt.Println()
	fmt.Println(renderSourcesHint(cs, sourceFilter))

	return nil
}

func runConfigShowMachine(format string, sourceFilter []string, groupFilter string) error {
	cs := loadConfigSources()
	settings := viper.AllSettings()

	if groupFilter != "" {
		if sub, ok := settings[groupFilter]; ok {
			settings = map[string]any{groupFilter: sub}
		} else {
			return fmt.Errorf("unknown group %q", groupFilter)
		}
	}

	// Apply source filter.
	if len(sourceFilter) > 0 {
		flat := flattenMap("", settings)
		filtered := make(map[string]any)
		for key, val := range flat {
			_, src := cs.resolveKeySource(key)
			if src == "" {
				// Try schema lookup.
				if ck, gn, ok := findConfigKey(key); ok {
					_, src = cs.resolveSource(gn, ck)
				}
			}
			if matchesSourceFilter(src, sourceFilter) {
				filtered[key] = val
			}
		}
		settings = filtered
	}

	switch format {
	case "yaml":
		printYAML(settings)
	case "json":
		printJSON(settings)
	case "env":
		printEnv(settings)
	default:
		return fmt.Errorf("unknown output format %q (valid: yaml, json, env)", format)
	}
	return nil
}

func buildConfigTree(cs *configSources, sourceFilter []string, groupFilter string) *ui.Tree {
	tree := ui.NewTree()

	// Collect all effective config as a flat map, then inject schema
	// defaults for keys that aren't explicitly set so the tree shows
	// the full effective configuration.
	allSettings := viper.AllSettings()
	injectSchemaDefaults(allSettings)

	// Determine which top-level keys are groups (have nested maps).
	topLevel := make(map[string]bool) // true = group, false = leaf
	for k, v := range allSettings {
		if _, ok := v.(map[string]any); ok {
			topLevel[k] = true
		} else {
			topLevel[k] = false
		}
	}

	// Sort keys for stable output.
	keys := make([]string, 0, len(topLevel))
	for k := range topLevel {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if groupFilter != "" && key != groupFilter {
			continue
		}

		isGroup := topLevel[key]
		if isGroup {
			children := buildGroupChildren(cs, key, allSettings[key].(map[string]any), sourceFilter)
			if len(children) == 0 && len(sourceFilter) > 0 {
				continue
			}
			group := ui.Group(key, children...)
			tree.Add(group)
		} else {
			node := buildLeafNode(cs, key, sourceFilter)
			if node == nil {
				continue
			}
			tree.Add(node)
		}
	}

	return tree
}

// injectSchemaDefaults adds schema keys into the settings map when
// they aren't already present but have an effective value (a default
// or a set env var), so the tree reflects the full effective config.
func injectSchemaDefaults(settings map[string]any) {
	for groupName, group := range configSchema {
		for _, ck := range group.Keys {
			// Determine the effective value for missing keys.
			val := ck.Default
			if val == "" && ck.EnvVar != "" {
				if v := os.Getenv(ck.EnvVar); v != "" {
					val = v
				}
			}
			if val == "" {
				// Also check automatic BOSUN_* env var.
				fk := fullKey(groupName, ck)
				if v := os.Getenv(envVarForKey(fk)); v != "" {
					val = v
				}
			}
			if val == "" {
				continue
			}

			fk := fullKey(groupName, ck)
			parts := strings.SplitN(fk, ".", 2)
			if len(parts) == 1 {
				if _, exists := settings[fk]; !exists {
					settings[fk] = val
				}
			} else {
				parent := parts[0]
				child := parts[1]
				sub, ok := settings[parent].(map[string]any)
				if !ok {
					sub = make(map[string]any)
					settings[parent] = sub
				}
				if _, exists := sub[child]; !exists {
					sub[child] = val
				}
			}
		}
	}
}

func buildGroupChildren(cs *configSources, groupKey string, m map[string]any, sourceFilter []string) []*ui.TreeNode {
	var children []*ui.TreeNode

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, childKey := range keys {
		childVal := m[childKey]
		fk := groupKey + "." + childKey

		if subMap, ok := childVal.(map[string]any); ok {
			// Nested group.
			subChildren := buildGroupChildren(cs, fk, subMap, sourceFilter)
			if len(subChildren) == 0 && len(sourceFilter) > 0 {
				continue
			}
			children = append(children, ui.Group(childKey, subChildren...))
			continue
		}

		// Leaf within group.
		value, source := resolveKeyWithSchema(cs, fk)
		if !matchesSourceFilter(source, sourceFilter) {
			continue
		}
		if value == "" {
			continue
		}
		// Mask secrets.
		if ck, _, ok := findConfigKey(fk); ok && ck.Secret {
			value = "••••••••"
		}
		glyph, glyphColor := sourceGlyph(source)
		children = append(children, ui.Leaf(glyph, glyphColor, childKey, value))
	}

	return children
}

func buildLeafNode(cs *configSources, key string, sourceFilter []string) *ui.TreeNode {
	value, source := resolveKeyWithSchema(cs, key)
	if !matchesSourceFilter(source, sourceFilter) {
		return nil
	}
	if value == "" {
		return nil
	}
	// Mask secrets.
	if ck, _, ok := findConfigKey(key); ok && ck.Secret {
		value = "••••••••"
	}
	glyph, glyphColor := sourceGlyph(source)
	return ui.Leaf(glyph, glyphColor, key, formatValue(value))
}

// resolveKeyWithSchema resolves a fully-qualified key, using schema
// metadata if available, falling back to raw source resolution.
func resolveKeyWithSchema(cs *configSources, key string) (value, source string) {
	if ck, gn, ok := findConfigKey(key); ok {
		return cs.resolveSource(gn, ck)
	}
	return cs.resolveKeySource(key)
}

// sourceGlyph returns the glyph and color for a config source tier.
func sourceGlyph(source string) (string, color.Color) {
	switch source {
	case sourceGlobal:
		return glyphGlobal, ui.Palette.Primary
	case sourceProject:
		return glyphProject, ui.Palette.Success
	case sourceEnv:
		return glyphEnv, ui.Palette.Warning
	default:
		return glyphDefault, ui.Palette.Muted
	}
}

// renderSourcesHint builds a single-line sources footer styled like
// huh keyboard hints: glyph label · glyph label · ...
func renderSourcesHint(cs *configSources, filter []string) string {
	labelStyle := lipgloss.NewStyle().Foreground(ui.Palette.Subtle)
	sepStyle := lipgloss.NewStyle().Foreground(ui.Palette.Recessed)
	glyphFor := func(c color.Color, g string) string {
		return lipgloss.NewStyle().Foreground(c).Render(g)
	}

	var parts []string
	if matchesSourceFilter(sourceDefault, filter) {
		parts = append(parts, glyphFor(ui.Palette.Muted, glyphDefault)+" "+labelStyle.Render("defaults"))
	}
	if cs.globalPath != "" && matchesSourceFilter(sourceGlobal, filter) {
		parts = append(parts, glyphFor(ui.Palette.Primary, glyphGlobal)+" "+labelStyle.Render(shortPath(cs.globalPath)))
	}
	if cs.projectPath != "" && matchesSourceFilter(sourceProject, filter) {
		parts = append(parts, glyphFor(ui.Palette.Success, glyphProject)+" "+labelStyle.Render(shortPath(cs.projectPath)))
	}
	if matchesSourceFilter(sourceEnv, filter) {
		if envCount := countEnvSources(cs); envCount > 0 {
			label := fmt.Sprintf("%d var", envCount)
			if envCount != 1 {
				label += "s"
			}
			parts = append(parts, glyphFor(ui.Palette.Warning, glyphEnv)+" "+labelStyle.Render(label))
		}
	}

	sep := " " + sepStyle.Render("·") + " "
	return "  " + strings.Join(parts, sep)
}

// shortPath replaces the home directory prefix with ~.
func shortPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if strings.HasPrefix(path, home) {
			return "~" + path[len(home):]
		}
	}
	return path
}

// matchesSourceFilter reports whether a source matches the active
// filter. An empty filter matches everything.
func matchesSourceFilter(source string, filter []string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == source {
			return true
		}
	}
	return false
}

// formatValue formats a config value for display, handling slices.
func formatValue(v string) string {
	// Viper renders slices as "[a b c]". Convert to comma-separated.
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		inner := v[1 : len(v)-1]
		if inner != "" {
			return strings.ReplaceAll(inner, " ", ", ")
		}
	}
	return v
}

// countEnvSources counts how many env vars contribute to the config.
func countEnvSources(cs *configSources) int {
	seen := make(map[string]bool)

	// Count BOSUN_* env vars.
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BOSUN_") {
			name := env[:strings.IndexByte(env, '=')]
			seen[name] = true
		}
	}

	// Count schema-specific env vars (e.g., GITHUB_TOKEN).
	for _, group := range configSchema {
		for _, ck := range group.Keys {
			if ck.EnvVar != "" && !strings.HasPrefix(ck.EnvVar, "BOSUN_") {
				if os.Getenv(ck.EnvVar) != "" {
					seen[ck.EnvVar] = true
				}
			}
		}
	}

	return len(seen)
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		Annotations: map[string]string{
			"output": "raw",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			val := viper.Get(key)
			if val == nil {
				// Check schema default.
				if ck, _, ok := findConfigKey(key); ok && ck.Default != "" {
					fmt.Println(ck.Default)
					return nil
				}
				return fmt.Errorf("key %q not set", key)
			}
			fmt.Println(val)
			return nil
		},
	}
}

func newConfigCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check [group]",
		Short: "Validate configuration completeness",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "check",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCard(cmd).Print()
			return runConfigCheck(args)
		},
	}
}

func runConfigCheck(args []string) error {
	var groupFilter string
	if len(args) > 0 {
		groupFilter = args[0]
	}

	passed, warned := 0, 0
	for name, group := range configSchema {
		if groupFilter != "" && name != groupFilter {
			continue
		}
		missing := checkGroupCompleteness(name, group)
		if len(missing) == 0 {
			ui.Complete(group.Label)
			passed++
		} else {
			ui.Skip(fmt.Sprintf("%s: missing %s", group.Label, strings.Join(missing, ", ")))
			warned++
		}
	}

	parts := []string{fmt.Sprintf("%d passed", passed)}
	if warned > 0 {
		parts = append(parts, fmt.Sprintf("%d incomplete", warned))
	}
	ui.Info("%s", strings.Join(parts, ", "))
	return nil
}

// checkGroupCompleteness returns the names of missing required keys
// in a config group.
func checkGroupCompleteness(groupName string, group ConfigGroup) []string {
	var missing []string
	for _, ck := range group.Keys {
		if !ck.Required {
			continue
		}
		fk := fullKey(groupName, ck)
		if viper.GetString(fk) == "" && ck.Default == "" {
			missing = append(missing, ck.Key)
		}
	}
	return missing
}

// Machine-readable output helpers.

func printYAML(settings map[string]any) {
	printYAMLMap(settings, 0)
}

func printYAMLMap(m map[string]any, indent int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	prefix := strings.Repeat("  ", indent)
	for _, k := range keys {
		v := m[k]
		switch val := v.(type) {
		case map[string]any:
			fmt.Printf("%s%s:\n", prefix, k)
			printYAMLMap(val, indent+1)
		default:
			fmt.Printf("%s%s: %v\n", prefix, k, val)
		}
	}
}

func printJSON(settings map[string]any) {
	printJSONValue(settings, 0, false)
	fmt.Println()
}

func printJSONValue(v any, indent int, inArray bool) {
	prefix := strings.Repeat("  ", indent)
	switch val := v.(type) {
	case map[string]any:
		fmt.Println("{")
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			fmt.Printf("%s  %q: ", prefix, k)
			printJSONValue(val[k], indent+1, false)
			if i < len(keys)-1 {
				fmt.Print(",")
			}
			fmt.Println()
		}
		fmt.Printf("%s}", prefix)
	case []any:
		fmt.Println("[")
		for i, item := range val {
			fmt.Printf("%s  ", prefix)
			printJSONValue(item, indent+1, true)
			if i < len(val)-1 {
				fmt.Print(",")
			}
			fmt.Println()
		}
		fmt.Printf("%s]", prefix)
	case string:
		fmt.Printf("%q", val)
	default:
		fmt.Printf("%v", val)
	}
}

func printEnv(settings map[string]any) {
	flat := flattenMap("", settings)
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		envKey := "BOSUN_" + strings.ToUpper(strings.ReplaceAll(k, ".", "_"))
		fmt.Printf("%s=%s\n", envKey, flat[k])
	}
}
