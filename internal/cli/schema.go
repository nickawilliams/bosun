package cli

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
	Source   func() ([]SourceOption, error) // Dynamic value source for interactive picker.
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
	"blocked",
	"review",
	"preview",
	"ready_for_release",
	"acceptance",
}

// configSchema is the central registry of all known config keys.
var configSchema = map[string]ConfigGroup{
	"issue_tracker": {
		Label: "issue tracker",

		Keys: []ConfigKey{
			{Key: "issue_tracker", Label: "provider", Options: []string{"jira"}, Required: true},
		},
	},
	"jira": {
		Label: "jira",

		Keys: []ConfigKey{
			{Key: "base_url", Label: "base URL", Example: "https://mycompany.atlassian.net", Required: true},
			{Key: "email", Label: "email", Required: true},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_JIRA_TOKEN", Secret: true, Required: true},
			{Key: "project", Label: "project key", Example: "PROJ"},
			{Key: "board_id", Label: "board ID", Example: "123"},
		},
	},
	"statuses": {
		Label: "status mappings",

		Keys: []ConfigKey{
			{Key: "ready", Label: "ready", Default: "Ready"},
			{Key: "in_progress", Label: "in progress", Default: "In Progress"},
			{Key: "blocked", Label: "blocked", Default: "Blocked"},
			{Key: "review", Label: "review", Default: "Review"},
			{Key: "preview", Label: "in preview env", Default: "In Preview Env"},
			{Key: "ready_for_release", Label: "ready for release", Default: "Ready for Release"},
			{Key: "acceptance", Label: "acceptance", Default: "Acceptance"},
			{Key: "done", Label: "done", Default: "Done"},
		},
	},
	"branch": {
		Label: "branch naming",

		Keys: []ConfigKey{
			{Key: "template", Label: "branch template", Default: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"},
			{Key: "categories.story", Label: "story category", Default: "feature"},
			{Key: "categories.bug", Label: "bug category", Default: "fix"},
			{Key: "categories.task", Label: "task category", Default: "chore"},
		},
	},
	"workspace": {
		Label: "workspace",

		Keys: []ConfigKey{
			{Key: "workspace_root", Label: "workspace root", Example: ".workspaces"},
		},
	},
	"code_host": {
		Label: "code host",

		Keys: []ConfigKey{
			{Key: "code_host", Label: "provider", Options: []string{"github"}, Required: true},
		},
	},
	"github": {
		Label: "GitHub",

		Keys: []ConfigKey{
			{Key: "token", Label: "personal access token", EnvVar: "GITHUB_TOKEN", Secret: true, Required: true},
		},
	},
	"pull_request": {
		Label: "pull request",

		Keys: []ConfigKey{
			{Key: "base", Label: "base branch", Default: "main"},
			{Key: "title_template", Label: "PR title template", Default: "[{{.IssueKey}}] {{.IssueTitle}}"},
			{Key: "body_template", Label: "PR body template"},
			{Key: "reviewers", Label: "reviewers (GitHub usernames)"},
			{Key: "team_reviewers", Label: "team reviewers (GitHub team slugs)"},
			{Key: "assignees", Label: "assignees (GitHub usernames)"},
			{Key: "self_assign", Label: "auto-assign PR author", Default: "true"},
		},
	},
	"notification": {
		Label: "notification",

		Keys: []ConfigKey{
			{Key: "notification", Label: "provider", Options: []string{"slack"}},
		},
	},
	"slack": {
		Label: "slack",

		Keys: []ConfigKey{
			{Key: "auth", Label: "auth method", Options: []string{"token", "local"}, Default: "token"},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_SLACK_TOKEN", Secret: true},
			{Key: "workspace", Label: "workspace name", Example: "mycompany"},
			{Key: "channel_review", Label: "review channel", Example: "bb-prs"},
			{Key: "channel_release", Label: "release channel", Example: "release_coordination"},
		},
	},
	"cicd": {
		Label: "CI/CD",

		Keys: []ConfigKey{
			{Key: "cicd", Label: "provider", Options: []string{"github_actions"}},
		},
	},
	"github_actions": {
		Label: "GitHub Actions",

		Keys: []ConfigKey{
			{Key: "workflows.preview", Label: "preview workflow", Example: "org/repo/.github/workflows/deploy-preview.yml"},
			{Key: "workflows.release", Label: "release workflow", Example: "org/repo/.github/workflows/deploy.yml"},
			{Key: "service_input", Label: "service input parameter", Default: "services-to-deploy"},
		},
	},
	"color_mode": {
		Label: "color mode",

		Keys: []ConfigKey{
			{Key: "color_mode", Label: "color mode", Options: []string{"truecolor", "ansi", "none"}, Default: "truecolor"},
		},
	},
	"display_mode": {
		Label: "display mode",

		Keys: []ConfigKey{
			{Key: "display_mode", Label: "display mode", Options: []string{"compact", "comfy"}, Default: "compact"},
		},
	},
}

// registerSource sets a Source function on a ConfigKey within a group.
// Called from init() to avoid package-level initialization cycles.
func registerSource(group, key string, source func() ([]SourceOption, error)) {
	g := configSchema[group]
	for i := range g.Keys {
		if g.Keys[i].Key == key {
			g.Keys[i].Source = source
		}
	}
	configSchema[group] = g
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
