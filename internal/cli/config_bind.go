package cli

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/services"
	"github.com/spf13/viper"
)

// loadConfig loads the merged config files and registers the schema
// with viper. Every code path that (re)loads configuration goes through
// here rather than calling config.Load directly: registration is what
// makes a bare viper.Get* read walk env → project → global → default,
// so a load without it would leave every read blind to the environment
// again. config.Load stays file-only because the schema lives on this
// side of the boundary — it aliases provider vocabulary and splices in
// registry-contributed keys, none of which internal/config may import.
func loadConfig() error {
	if err := config.Load(); err != nil {
		return err
	}
	bindSchemaEnv()
	return nil
}

// bindSchemaEnv registers the config schema with viper: env-var
// bindings for every scalar key, and schema defaults.
//
// With this in place a single viper.Get*(key) resolves the full
// precedence ladder, which is what lets `config check`, requireConfig,
// and every bare read in the command layer share one resolution path
// instead of each reimplementing it (the pre-#114 state: five copies
// of the env lookup, two of them disagreeing on precedence).
//
// Map-shaped groups are the deliberate exception: their env form is
// decoded by mapGroupValues from the same bindings table, never
// registered with viper. Registering a name makes viper's AllKeys
// enumeration treat that path as a flat key and stop descending, so a
// bound group path would hide the group's per-child defaults — and any
// file children beneath a deeper bound name — from AllSettings, which
// is the surface `config show` and `config get` render. (Measured on
// viper v1.21.0: with the group path bound and its variable unset,
// AllKeys collapses to the group path and a child registered via
// SetDefault vanishes from AllSettings while a direct Get still
// resolves it.)
//
// Two passes, in a load-bearing order. The bindings pass is static —
// it walks the declared schema and the provider registry, reading no
// configuration — so it can run first. The defaults pass iterates
// schemaGroups(), which resolves each group's configured provider via
// a viper read; running it second means a provider selected through
// its own env var (BOSUN_ISSUE_TRACKER_PROVIDER) is already visible.
//
// Idempotent in effect: re-running after a reload re-registers the
// same names (viper appends duplicates to a key's env list, which is
// harmless — the same variables are consulted in the same order) and
// re-applies the same defaults. AutomaticEnv stays off; it and BindEnv
// do not compose (see config.Load).
func bindSchemaEnv() {
	mapGroups := mapGroupPaths()
	for key, names := range schemaEnvBindings() {
		if mapGroups[key] {
			continue
		}
		_ = viper.BindEnv(append([]string{key}, names...)...)
	}

	for groupName, group := range schemaGroups() {
		for _, ck := range group.Keys {
			// The provider key's Default is computed, not declared:
			// resolveSchemaGroup fills it with the sole registered
			// provider so prompts and `config show` can display what
			// an unset key resolves to. It stays out of viper's
			// default layer because "unset" is an observable state the
			// key must keep — doctor reads it as "this integration
			// isn't configured, don't probe it", and the construction
			// path applies the same sole-provider fallback internally
			// (registry.configured). A registered default would erase
			// the distinction and make every absent integration read
			// as a configured one. Display still shows it: see
			// effectiveSettings.
			if ck.Key == "provider" {
				continue
			}
			if ck.Default != "" {
				viper.SetDefault(fullKey(groupName, ck), ck.Default)
			}
		}
	}
}

// schemaEnvBindings returns the env-var names each config key resolves
// from, keyed by fully-qualified key, in resolution order: the key's
// explicit EnvVar (GITHUB_TOKEN) first, the computed BOSUN_* name
// second. viper.BindEnv tries names in order, so registering this
// table verbatim gives the same semantics the retired effectiveEnvValue
// implemented by hand.
//
// It is the single authority on what the environment can address —
// bindSchemaEnv registers the scalar entries, mapGroupValues decodes
// the map-group entries, and the source-attribution paths probe all of
// it — so display and resolution cannot disagree about which variables
// are live.
//
// Two shapes, one rule each:
//
//   - A scalar key binds at its own path, under both env names.
//   - A map-shaped group (MapKey set) is addressed at the GROUP path
//     only, as one key of map type: the variable carries the whole map
//     as JSON (BOSUN_ISSUE_TRACKER_STATUSES='{"ready":"Backlog"}'), and
//     the group's declared keys are members of that map rather than
//     independent env targets. Addressing the members individually
//     would recreate the split this closes — a per-child variable
//     visible to one read shape and invisible to the other — because
//     env supplies a value at one key and nothing decomposes it into
//     child paths.
//
// Provider-contributed keys are taken as the union across EVERY
// registered provider, not just the configured one — the same
// reasoning secretKeys and schemaKeySurface document. The union also
// breaks a cycle: which provider is configured can itself arrive via
// env, so resolving it here would need the very bindings this builds.
// A bound name whose provider isn't configured is inert — nothing
// reads its key.
//
// Memoized: the table depends only on the declared schema and the
// provider registry, both immutable after package init.
var schemaEnvBindings = sync.OnceValue(func() map[string][]string {
	out := make(map[string][]string)
	bind := func(groupName string, ck ConfigKey) {
		fk := fullKey(groupName, ck)
		if ck.EnvVar != "" {
			out[fk] = appendMissing(out[fk], ck.EnvVar)
		}
		out[fk] = appendMissing(out[fk], envVarForKey(fk))
	}

	// The declared tree, unresolved: sub-groups are reached without
	// depending on which provider is configured, and the splice marker
	// is still present so it has to be skipped by name.
	declared := make(map[string]ConfigGroup)
	for name, group := range configSchema {
		flattenSchemaGroup(name, group, declared)
	}
	for groupName, group := range declared {
		if group.MapKey != "" {
			out[groupName] = appendMissing(out[groupName], envVarForKey(groupName))
			continue
		}
		for _, ck := range group.Keys {
			if ck.Key == providerKeysMarker {
				continue
			}
			bind(groupName, ck)
		}
	}

	for groupName := range configSchema {
		for _, name := range services.ProviderNames(groupName) {
			for _, ck := range services.ProviderKeys(groupName, name) {
				bind(groupName, ck)
			}
		}
	}

	return out
})

// mapGroupPaths returns the dotted paths of every map-shaped schema
// group. Memoized alongside schemaEnvBindings for the same reason: the
// declared tree is immutable after package init.
var mapGroupPaths = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool)
	declared := make(map[string]ConfigGroup)
	for name, group := range configSchema {
		flattenSchemaGroup(name, group, declared)
	}
	for groupName, group := range declared {
		if group.MapKey != "" {
			out[groupName] = true
		}
	}
	return out
})

// appendMissing appends name to names unless already present. The
// bindings table is small, so the linear scan beats carrying a set
// alongside every slice.
func appendMissing(names []string, name string) []string {
	if slices.Contains(names, name) {
		return names
	}
	return append(names, name)
}

// boundEnvValue returns the value the environment supplies for a
// fully-qualified key, or "" — the attribution-side mirror of the
// bindings viper resolves values through. Source attribution can't ask
// viper where a value came from (Get returns the value alone), so the
// `config show` / `config check` paths probe the same table
// bindSchemaEnv registered. A key the table doesn't name — an unknown
// key, or a member of a map-shaped group — has no env binding, and
// correctly attributes to its file layer even when a same-named
// variable happens to be exported.
func boundEnvValue(key string) string {
	for _, name := range schemaEnvBindings()[key] {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// mapGroupEnvValue returns the env-supplied map for a map-shaped group
// — the group's variable decoded as JSON — or nil when the variable is
// unset or does not decode. An undecodable value is treated as unset
// rather than as an empty map, so an operator's malformed JSON leaves
// the file's own mappings in force instead of silently reverting the
// whole group to its defaults.
func mapGroupEnvValue(groupPath string) map[string]string {
	raw := boundEnvValue(groupPath)
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// mapGroupValues returns the effective value of a map-shaped schema
// group as one map: the group's declared defaults, overlaid by the
// env-supplied JSON map when the group's variable is set, or by the
// file children otherwise. Env overrides the block wholesale — the two
// sources don't interleave — with the schema defaults backstopping
// members neither names.
//
// This is the accessor every consumer of a map-shaped key goes
// through. Reading a member by string-concatenated child path instead
// would silently ignore the env form: env supplies the whole map at
// the group's one key, and nothing decomposes it into child paths, so
// the two read shapes disagree exactly when the variable is set.
//
// The env decode happens here rather than through a viper binding
// because registering the group path with viper poisons the AllKeys
// enumeration the display path renders from — see bindSchemaEnv. The
// defaults overlay lives here rather than relying on viper's default
// layer for the group because viper resolves the group key from the
// highest-precedence layer that has it, without merging maps across
// layers — a config file setting one status would otherwise hide the
// defaults for the other seven.
func mapGroupValues(groupPath string) map[string]string {
	out := make(map[string]string)
	if group, ok := lookupGroup(groupPath); ok {
		for _, ck := range group.Keys {
			if ck.Default != "" {
				out[ck.Key] = ck.Default
			}
		}
	}
	if env := mapGroupEnvValue(groupPath); env != nil {
		maps.Copy(out, env)
		return out
	}
	maps.Copy(out, viper.GetStringMapString(groupPath))
	return out
}
