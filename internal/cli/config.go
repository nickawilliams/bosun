package cli

import (
	"fmt"
	"image/color"
	"os"
	"slices"
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
		// Bare `bosun config` (with optional flags) runs `show`.
		// NoArgs rejects positional args so a typo'd subcommand
		// surfaces as "unknown command" rather than silently being
		// forwarded to show as a group filter — and so a future
		// schema group can never shadow a real subcommand name.
		Args: cobra.NoArgs,
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

	// Inherit show's `-g` on the parent so `bosun config -g` works
	// (matches the parent's bare RunE delegating to runConfigShow).
	addProjectFlag(cmd)
	cmd.Flags().BoolP("global", "g", false, "show the global config only (skip project, env, and defaults)")

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

	addProjectFlag(cmd)
	cmd.Flags().BoolP("global", "g", false, "show the global config only (skip project, env, and defaults)")

	return cmd
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	globalOnly, _ := cmd.Flags().GetBool("global")
	var groupFilter string
	if len(args) > 0 {
		groupFilter = args[0]
	}

	if groupFilter != "" && !isKnownConfigGroup(groupFilter) {
		return fmt.Errorf("unknown config group %q", groupFilter)
	}

	cs := loadConfigSources()

	tree := buildConfigTree(cs, groupFilter, globalOnly)
	if tree.IsEmpty() {
		ui.EmptyState("no config values to display")
	} else {
		tree.Print()
	}

	fmt.Println()
	fmt.Println(renderSourcesHint(cs, globalOnly))

	return nil
}

// isKnownConfigGroup reports whether name is a top-level group that
// the config tree would actually render — either a key present in
// the effective viper settings or a schema-known group with default/
// env-derived values injected by injectSchemaDefaults. Mirrors what
// buildConfigTree iterates so validation and rendering agree.
func isKnownConfigGroup(name string) bool {
	settings := viper.AllSettings()
	injectSchemaDefaults(settings)
	_, ok := settings[name]
	return ok
}

func buildConfigTree(cs *configSources, groupFilter string, globalOnly bool) *ui.Tree {
	tree := ui.NewTree()

	// Default view shows the effective config: viper's merged result
	// with schema defaults injected so unset-but-known keys still
	// render. `-g` narrows the view to what's literally in the global
	// config file — no project, no env, no defaults.
	var allSettings map[string]any
	if globalOnly {
		if cs.global == nil {
			return tree
		}
		allSettings = cs.global.AllSettings()
	} else {
		allSettings = viper.AllSettings()
		injectSchemaDefaults(allSettings)
	}

	topLevel := make(map[string]bool) // true = group, false = leaf
	for k, v := range allSettings {
		if _, ok := v.(map[string]any); ok {
			topLevel[k] = true
		} else {
			topLevel[k] = false
		}
	}

	keys := make([]string, 0, len(topLevel))
	for k := range topLevel {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if groupFilter != "" && key != groupFilter {
			continue
		}

		if topLevel[key] {
			children := buildGroupChildren(cs, key, allSettings[key].(map[string]any))
			tree.Add(ui.Group(key, children...))
		} else if node := buildLeafNode(cs, key); node != nil {
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
				if !nestedKeyExists(sub, child) {
					setNestedKey(sub, child, val)
				}
			}
		}
	}
}

// setNestedKey writes a value into a nested map at a dot-separated
// path, creating intermediate maps as needed. For "statuses.ready"
// it sets m["statuses"]["ready"] = val.
func setNestedKey(m map[string]any, key string, val any) {
	parts := strings.Split(key, ".")
	current := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := current[p].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[p] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = val
}

// nestedKeyExists checks whether a dot-separated key already exists
// in a nested map. For "categories.bug" it walks m["categories"]["bug"].
func nestedKeyExists(m map[string]any, key string) bool {
	parts := strings.Split(key, ".")
	current := m
	for i, p := range parts {
		v, ok := current[p]
		if !ok {
			return false
		}
		if i == len(parts)-1 {
			return true
		}
		next, ok := v.(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return false
}

func buildGroupChildren(cs *configSources, groupKey string, m map[string]any) []*ui.TreeNode {
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
			children = append(children, ui.Group(childKey, buildGroupChildren(cs, fk, subMap)...))
			continue
		}

		// Leaf within group.
		value, source := resolveKeyWithSchema(cs, fk)
		if value == "" {
			continue
		}
		if ck, _, ok := findConfigKey(fk); ok && ck.Secret {
			value = secretMask
		}
		glyph, glyphColor := sourceGlyph(source)
		children = append(children, ui.Leaf(glyph, glyphColor, childKey, value))
	}

	return children
}

func buildLeafNode(cs *configSources, key string) *ui.TreeNode {
	value, source := resolveKeyWithSchema(cs, key)
	if value == "" {
		return nil
	}
	// Mask secrets.
	if ck, _, ok := findConfigKey(key); ok && ck.Secret {
		value = secretMask
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
// `globalOnly` mirrors the show command's `-g` flag — when set, only
// the global row appears so the legend matches the narrowed tree.
func renderSourcesHint(cs *configSources, globalOnly bool) string {
	labelStyle := lipgloss.NewStyle().Foreground(ui.Palette.Subtle)
	sepStyle := lipgloss.NewStyle().Foreground(ui.Palette.Recessed)
	glyphFor := func(c color.Color, g string) string {
		return lipgloss.NewStyle().Foreground(c).Render(g)
	}

	var parts []string
	if globalOnly {
		if cs.globalPath != "" {
			parts = append(parts, glyphFor(ui.Palette.Primary, glyphGlobal)+" "+labelStyle.Render(shortPath(cs.globalPath)))
		}
	} else {
		parts = append(parts, glyphFor(ui.Palette.Muted, glyphDefault)+" "+labelStyle.Render("defaults"))
		if cs.globalPath != "" {
			parts = append(parts, glyphFor(ui.Palette.Primary, glyphGlobal)+" "+labelStyle.Render(shortPath(cs.globalPath)))
		}
		if cs.projectPath != "" {
			parts = append(parts, glyphFor(ui.Palette.Success, glyphProject)+" "+labelStyle.Render(shortPath(cs.projectPath)))
		}
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
	cmd := &cobra.Command{
		Use:   "get [key]",
		Short: "Get a configuration value",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			"output":              "raw",
			headerAnnotationTitle: "get value",
		},
		RunE: runConfigGet,
	}

	addProjectFlag(cmd)
	cmd.Flags().BoolP("global", "g", false, "read from the global config only (skip project, env, and defaults)")
	cmd.Flags().StringP("format", "f", "raw", "output format: raw, yaml, json, env")

	return cmd
}

// runConfigGet dispatches on (key, globalOnly, format). The matrix:
//
//   - no key + format=raw           → error (need a key or a non-raw format)
//   - no key + yaml|json|env        → full settings rendered in that format
//   - key + raw                     → scalar value; error if non-scalar
//   - key + yaml|json|env           → that key's subtree rendered
//
// Effective vs. global-only is governed by `-g`: without it the full
// viper merge + schema defaults are used (the normal "what does the
// app see" view); with it only the global config file is consulted,
// and a key that's missing exits 0 silently — the convention for
// "scope-aware read returns nothing" scripting patterns.
func runConfigGet(cmd *cobra.Command, args []string) error {
	globalOnly, _ := cmd.Flags().GetBool("global")
	format, _ := cmd.Flags().GetString("format")

	switch format {
	case "raw", "yaml", "json", "env":
		// ok
	default:
		return fmt.Errorf("unknown format %q (valid: raw, yaml, json, env)", format)
	}

	settings, ok := getSettings(globalOnly)
	if !ok {
		// `-g` with no global file present.
		if len(args) > 0 && globalOnly && format == "raw" {
			return nil // silent exit 0 for scope-aware miss
		}
		settings = map[string]any{}
	}

	if len(args) == 0 {
		if format == "raw" {
			return fmt.Errorf("specify a key or -f yaml|json|env")
		}
		maskSecrets(settings)
		renderSettings(settings, format)
		return nil
	}

	key := args[0]
	val := lookupNested(settings, key)
	if val == nil {
		if globalOnly {
			return nil // silent exit 0 for scope-aware miss
		}
		return fmt.Errorf("key %q not set", key)
	}

	if format == "raw" {
		if _, isMap := val.(map[string]any); isMap {
			return fmt.Errorf("%q is a group; use -f yaml|json|env", key)
		}
		fmt.Println(val)
		return nil
	}

	// Render the subtree under `key` in the requested format. Masked
	// first — only the raw exact-key path above returns real secret
	// values. Wrap scalar values back into a single-entry map so the
	// format helpers (which expect a map) handle both shapes
	// uniformly.
	maskSecrets(settings)
	val = lookupNested(settings, key)
	var subtree map[string]any
	if m, isMap := val.(map[string]any); isMap {
		subtree = map[string]any{key: m}
	} else {
		subtree = map[string]any{key: val}
	}
	renderSettings(subtree, format)
	return nil
}

// secretMask is what renders in place of Secret-typed values anywhere
// output isn't an explicit request for the real value.
const secretMask = "••••••••"

// maskSecrets replaces every Secret-typed schema key's value in
// settings with the mask, in place. The machine formats land in pipes,
// logs, and CI output — exactly where a leaked token does the most
// damage — and injectSchemaDefaults pulls env-var tokens into the map,
// so values that were never written to any config file would otherwise
// print verbatim. An exact-key `config get` in raw format remains the
// deliberate escape hatch for scripts that need the real value.
func maskSecrets(settings map[string]any) {
	for groupName, group := range configSchema {
		for _, ck := range group.Keys {
			if !ck.Secret {
				continue
			}
			parts := strings.Split(fullKey(groupName, ck), ".")
			m := settings
			walked := true
			for _, part := range parts[:len(parts)-1] {
				next, isMap := m[part].(map[string]any)
				if !isMap {
					walked = false
					break
				}
				m = next
			}
			if !walked {
				continue
			}
			leaf := parts[len(parts)-1]
			if v, exists := m[leaf]; exists {
				if s, isStr := v.(string); !isStr || s != "" {
					m[leaf] = secretMask
				}
			}
		}
	}
}

// getSettings returns the settings map for the requested scope.
// Effective scope is viper's merged view with schema defaults applied.
// Global scope reads only the global config file; ok=false when no
// global file is present so callers can branch on "scope unreachable".
func getSettings(globalOnly bool) (settings map[string]any, ok bool) {
	if !globalOnly {
		s := viper.AllSettings()
		injectSchemaDefaults(s)
		return s, true
	}
	cs := loadConfigSources()
	if cs.global == nil {
		return nil, false
	}
	return cs.global.AllSettings(), true
}

// lookupNested walks a dot-separated key down into a nested settings
// map. Returns the leaf value (scalar or sub-map), or nil if any
// segment is missing.
func lookupNested(settings map[string]any, key string) any {
	parts := strings.Split(key, ".")
	var cur any = settings
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[p]
		if !ok {
			return nil
		}
	}
	return cur
}

// renderSettings dispatches to the right format helper. Caller
// validates the format string upstream.
func renderSettings(settings map[string]any, format string) {
	switch format {
	case "yaml":
		printYAML(settings)
	case "json":
		printJSON(settings)
	case "env":
		printEnv(settings)
	}
}

func newConfigCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [group]",
		Short: "Validate configuration completeness",
		Args:  cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "check",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigCheck(args)
		},
	}

	addProjectFlag(cmd)
	return cmd
}

// configIssueSeverity ranks schema violations so a group's row glyph
// reflects its worst issue and the summary's count buckets are clear.
type configIssueSeverity int

const (
	configWarn configIssueSeverity = iota
	configFail
)

// configIssue is one schema violation surfaced by validateGroup.
// Category buckets per-key issues for compact rendering ("missing: a,
// b" rather than one line per key); Detail carries the per-key
// rendering for categories where the key name alone isn't enough
// (e.g., "invalid" wants both key and bad value).
type configIssue struct {
	Key      string
	Severity configIssueSeverity
	Category string // "missing" | "invalid"
	Detail   string // for "invalid": "key=\"value\" (expected: a|b)"; empty for "missing"
}

// validateGroup walks every key in a schema group and returns the
// schema violations the resolved config has against it. Today the
// rules are minimal — Required keys must be set (or have a Default),
// and any key declaring Options must hold a value within that list —
// but new rules (Pattern, EnvVar resolution, cross-key consistency)
// land here without changing call sites.
func validateGroup(groupName string, group ConfigGroup) []configIssue {
	var issues []configIssue
	for _, ck := range group.Keys {
		// resolveConfigValue honors env vars (explicit EnvVar +
		// automatic BOSUN_*) in addition to viper's merged file
		// state. Previously this used bare viper.GetString, which
		// missed both the explicit-EnvVar tier and the BOSUN_*
		// computed names (viper's AutomaticEnv has no
		// `.`→`_` key replacer, so BOSUN_JIRA_TOKEN never
		// matched `jira.token`). A schema-required value provided
		// via env was therefore reported as "missing".
		value := resolveConfigValue(groupName, ck)

		if ck.Required && value == "" {
			issues = append(issues, configIssue{
				Key:      ck.Key,
				Severity: configFail,
				Category: "missing",
			})
			continue
		}

		if value != "" && len(ck.Options) > 0 && !slices.Contains(ck.Options, value) {
			severity := configWarn
			if ck.Required {
				severity = configFail
			}
			issues = append(issues, configIssue{
				Key:      ck.Key,
				Severity: severity,
				Category: "invalid",
				// Detail is the value-side context only — the key
				// itself becomes the leaf label downstream.
				Detail: fmt.Sprintf("%q (expected: %s)", value, strings.Join(ck.Options, "|")),
			})
		}
	}
	return issues
}

// severityGlyph maps a configIssueSeverity to its event-context
// glyph + color per state_grammar.go — ✗ red for fail, ▲ yellow for
// warn. Used by per-key tree leaves and (for the worst issue in a
// group) by the group's parent node.
func severityGlyph(s configIssueSeverity) (glyph string, glyphColor color.Color) {
	if s == configFail {
		return ui.Palette.Cross, ui.Palette.Error
	}
	return "▲", ui.Palette.Warning
}

// issueDetail returns the per-key tree-leaf value for an issue: a
// short string explaining what's wrong with this specific key. The
// key itself is the leaf label; this string is the dot-separated
// value beside it.
func issueDetail(iss configIssue) string {
	if iss.Category == "missing" {
		return "not set"
	}
	return iss.Detail
}

// worstSeverity returns the highest severity in issues. Caller has
// already established len(issues) > 0.
func worstSeverity(issues []configIssue) configIssueSeverity {
	worst := issues[0].Severity
	for _, iss := range issues[1:] {
		if iss.Severity > worst {
			worst = iss.Severity
		}
	}
	return worst
}

func runConfigCheck(args []string) error {
	var groupFilter string
	if len(args) > 0 {
		groupFilter = args[0]
	}

	// Stable iteration so the output is reproducible — configSchema
	// is a map, so we collect and sort the keys to lock in order.
	names := make([]string, 0, len(configSchema))
	for name := range configSchema {
		if groupFilter != "" && name != groupFilter {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	tree := ui.NewTree()
	passed, warned, failed := 0, 0, 0

	for _, name := range names {
		group := configSchema[name]
		issues := validateGroup(name, group)

		if len(issues) == 0 {
			// Passing: one-line leaf with "N/N keys" — tells the user
			// what was validated even when nothing failed.
			n := len(group.Keys)
			leaf := ui.Leaf(
				ui.Palette.Check, ui.Palette.Success,
				name,
				fmt.Sprintf("%d/%d keys", n, n),
			)
			leaf.ValueColor = ui.Palette.Success
			tree.Add(leaf)
			passed++
			continue
		}

		worst := worstSeverity(issues)
		if worst == configFail {
			failed++
		} else {
			warned++
		}

		// Failing: expand to per-issue children so each broken key
		// gets its own row instead of being crammed into a compound
		// "missing: a, b, c" string.
		children := make([]*ui.TreeNode, 0, len(issues))
		for _, iss := range issues {
			g, c := severityGlyph(iss.Severity)
			child := ui.Leaf(g, c, iss.Key, issueDetail(iss))
			child.ValueColor = c
			children = append(children, child)
		}
		groupNode := ui.Group(name, children...)
		groupNode.Glyph, groupNode.GlyphColor = severityGlyph(worst)
		tree.Add(groupNode)
	}

	// ContinuesBelow so the tree's last branch is ├── and the spine
	// flows into the summary card that follows — keeps the timeline
	// visually connected through the rollup.
	tree.ContinuesBelow().Print()

	// Summary card — segments ordered ascending by severity so the
	// last non-zero one drives the rollup glyph color, matching the
	// doctor command's pattern.
	total := passed + warned + failed
	ui.Default().Summary(
		fmt.Sprintf("%d %s", total, pluralize(total, "check", "checks")),
		[]ui.SummarySegment{
			{Count: passed, Label: "passed", Color: ui.Palette.Success},
			{Count: warned, Label: pluralize(warned, "warning", "warnings"), Color: ui.Palette.Warning},
			{Count: failed, Label: "failed", Color: ui.Palette.Error},
		},
	)
	return nil
}

// checkGroupCompleteness returns the names of missing required keys
// in a config group. Uses resolveConfigValue so env-provided values
// (explicit EnvVar or automatic BOSUN_*) count as "set" — same fix
// as validateGroup; doctor uses this for issue-tracker completeness.
func checkGroupCompleteness(groupName string, group ConfigGroup) []string {
	var missing []string
	for _, ck := range group.Keys {
		if !ck.Required {
			continue
		}
		if resolveConfigValue(groupName, ck) == "" {
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
