// Package provider holds the vocabulary shared between bosun's
// capability interfaces (issue.Tracker, code.Host, …), the provider
// adapters that implement them, and the configuration layer that feeds
// them.
//
// It exists so a provider adapter can declare what configuration it
// needs — and read it — without importing the CLI, viper, or the
// terminal UI. The CLI supplies a Config implementation; the adapter
// only ever sees keys and strings.
package provider

// Config is bosun's configuration as a provider adapter sees it:
// absolute-keyed value reads plus the just-in-time completion hook that
// fills in whatever is missing. The CLI implements it over viper and
// its interactive prompts; tests implement it over a map.
type Config interface {
	// Get returns the value configured for an absolute key
	// (e.g. "issue_tracker.base_url"), or "" when nothing is set.
	Get(key string) string

	// Require ensures every named config key or group is populated,
	// prompting the user when the session is interactive and returning
	// an error when a required value can't be resolved. Adapters call it
	// once their own credential discovery has come up empty.
	Require(keys ...string) error
}

// SourceOption represents a single option returned by a ConfigKey Source.
type SourceOption struct {
	Label string // Display text (e.g., "My Board (scrum, id: 42)").
	Value string // Stored value (e.g., "42").
}

// ConfigKey describes a single configuration value.
type ConfigKey struct {
	Key      string   // Config key (relative to group, e.g. "base_url").
	Label    string   // Human-readable label for prompts.
	Example  string   // Example value shown as placeholder.
	Default  string   // Default value if not set.
	Options  []string // Valid values (renders as select if non-empty).
	EnvVar   string   // Environment variable name (if value comes from env).
	Secret   bool     // Mask input (for tokens/passwords).
	Required bool     // Must have a value for the group to be valid.

	// Source is a dynamic value source for the interactive picker. Set
	// by the CLI (which owns the pickers), never by provider adapters.
	Source func() ([]SourceOption, error)
}

// ConfigGroup describes a related set of config values (e.g., "issue_tracker").
//
// Groups nest, mirroring the YAML they describe: "issue_tracker" holds a
// "statuses" sub-group, "preview" holds "up" and "down", each of which
// holds "inputs". Nesting is declared structurally rather than smuggled
// into dotted Key strings so that every level is addressable — a
// sub-group can carry its own Label, and MapKey applies to one level
// rather than to a key-name prefix.
type ConfigGroup struct {
	Label string      // Human-readable label (e.g., "issue tracker").
	Keys  []ConfigKey // The config keys in this group.

	// Name is the group's own segment, relative to its parent
	// ("statuses", "up", "inputs"). Empty at the top level, where the
	// registry's map key names the group.
	Name string

	// Groups are the nested sub-groups, in declaration order. Order is
	// authoritative for the same reason Keys order is: prompts, the
	// init form, and `config check` all walk the schema in order.
	Groups []ConfigGroup

	// MapKey, when non-empty, declares that this group is map-shaped:
	// beyond the keys named in Keys, the user chooses the key names.
	// The value is the placeholder for what a key names — "state" for
	// issue_tracker.statuses.<state>, "type" for
	// notification.templates.<type>. Keys may still be declared
	// alongside it, and are then the well-known members of an open set:
	// bosun ships defaults for the lifecycle statuses it drives, and a
	// tracker with a state bosun doesn't model can still be mapped.
	//
	// It exists for the unknown-key check, which otherwise has no way
	// to tell a user-chosen key from a typo.
	MapKey string
}
