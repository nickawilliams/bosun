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
type ConfigGroup struct {
	Label string      // Human-readable label (e.g., "issue tracker").
	Keys  []ConfigKey // The config keys in this group.
}
