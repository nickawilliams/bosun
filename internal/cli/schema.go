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

// lifecycleStatusKeys defines the canonical ordering of lifecycle
// stages. This sequence drives status sort order in the issue picker.
var lifecycleStatusKeys = []string{
	"ready",
	"in_progress",
	"review",
	"preview",
	"ready_for_release",
	"done",
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
			{Key: "template", Label: "Branch template", Default: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"},
			{Key: "categories.story", Label: "Story category", Default: "feature"},
			{Key: "categories.bug", Label: "Bug category", Default: "fix"},
			{Key: "categories.task", Label: "Task category", Default: "chore"},
		},
	},
	"workspace": {
		Label: "Workspace",

		Keys: []ConfigKey{
			{Key: "workspace_root", Label: "Workspace root", Example: ".workspaces"},
		},
	},
	"code_host": {
		Label: "Code host",

		Keys: []ConfigKey{
			{Key: "code_host", Label: "Provider", Options: []string{"github"}, Required: true},
		},
	},
	"github": {
		Label: "GitHub",

		Keys: []ConfigKey{
			{Key: "token", Label: "Personal access token", EnvVar: "GITHUB_TOKEN", Secret: true, Required: true},
		},
	},
	"pull_request": {
		Label: "Pull request",

		Keys: []ConfigKey{
			{Key: "base", Label: "Base branch", Default: "main"},
			{Key: "title_template", Label: "PR title template", Default: "[{{.IssueKey}}] {{.IssueTitle}}"},
			{Key: "body_template", Label: "PR body template"},
			{Key: "reviewers", Label: "Reviewers (GitHub usernames)"},
			{Key: "team_reviewers", Label: "Team reviewers (GitHub team slugs)"},
			{Key: "assignees", Label: "Assignees (GitHub usernames)"},
			{Key: "self_assign", Label: "Auto-assign PR author", Default: "true"},
		},
	},
	"notification": {
		Label: "Notification",

		Keys: []ConfigKey{
			{Key: "notification", Label: "Provider", Options: []string{"slack"}},
		},
	},
	"slack": {
		Label: "Slack",

		Keys: []ConfigKey{
			{Key: "auth", Label: "Auth method", Options: []string{"token", "local"}, Default: "token"},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_SLACK_TOKEN", Secret: true},
			{Key: "workspace", Label: "Workspace name", Example: "mycompany"},
			{Key: "channel_review", Label: "Review channel", Example: "bb-prs"},
			{Key: "channel_release", Label: "Release channel", Example: "release_coordination"},
		},
	},
	"color_mode": {
		Label: "Color mode",

		Keys: []ConfigKey{
			{Key: "color_mode", Label: "Color mode", Options: []string{"truecolor", "ansi", "none"}, Default: "truecolor"},
		},
	},
	"display_mode": {
		Label: "Display mode",

		Keys: []ConfigKey{
			{Key: "display_mode", Label: "Display mode", Options: []string{"compact", "comfy"}, Default: "compact"},
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
