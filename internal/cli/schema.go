package cli

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
}

// ConfigGroup describes a related set of config values (e.g., "jira").
type ConfigGroup struct {
	Label string      // Human-readable label (e.g., "Jira").
	Keys  []ConfigKey // The config keys in this group.
}

// configSchema is the central registry of all known config keys.
var configSchema = map[string]ConfigGroup{
	"issue_tracker": {
		Label: "Issue tracker",

		Keys: []ConfigKey{
			{Key: "issue_tracker", Label: "Provider", Options: []string{"jira"}, Required: true},
		},
	},
	"jira": {
		Label: "Jira",

		Keys: []ConfigKey{
			{Key: "base_url", Label: "Base URL", Example: "https://mycompany.atlassian.net", Required: true},
			{Key: "email", Label: "Email", Required: true},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_JIRA_TOKEN", Secret: true, Required: true},
			{Key: "project", Label: "Project key", Example: "PROJ"},
		},
	},
	"statuses": {
		Label: "Status mappings",

		Keys: []ConfigKey{
			{Key: "ready", Label: "Ready", Default: "Ready"},
			{Key: "in_progress", Label: "In Progress", Default: "In Progress"},
			{Key: "review", Label: "Review", Default: "Review"},
			{Key: "preview", Label: "In Preview Env", Default: "In Preview Env"},
			{Key: "ready_for_release", Label: "Ready for Release", Default: "Ready for Release"},
			{Key: "done", Label: "Done", Default: "Done"},
		},
	},
	"branch": {
		Label: "Branch naming",

		Keys: []ConfigKey{
			{Key: "pattern", Label: "Branch pattern", Default: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"},
			{Key: "categories.story", Label: "Story category", Default: "feature"},
			{Key: "categories.bug", Label: "Bug category", Default: "fix"},
			{Key: "categories.task", Label: "Task category", Default: "chore"},
		},
	},
	"workspace": {
		Label: "Workspace",

		Keys: []ConfigKey{
			{Key: "workspace_root", Label: "Workspace root", Default: "_workspaces"},
		},
	},
}

// lookupGroup returns the config group for a given name.
func lookupGroup(name string) (ConfigGroup, bool) {
	g, ok := configSchema[name]
	return g, ok
}

// fullKey returns the fully-qualified viper key for a group key.
// For top-level groups like "issue_tracker", the key is used as-is.
// For nested groups like "jira", the key is prefixed: "jira.base_url".
func fullKey(groupName string, key ConfigKey) string {
	// If the key already contains the group prefix or is the group itself,
	// use it as-is.
	if key.Key == groupName || len(groupName) == 0 {
		return key.Key
	}
	return groupName + "." + key.Key
}
