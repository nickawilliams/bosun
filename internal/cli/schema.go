package cli

import (
	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/provider"
	"github.com/nickawilliams/bosun/internal/services"
	"github.com/spf13/viper"
)

// The config-schema vocabulary is shared with the provider adapters,
// which contribute keys of their own (see provider.ConfigKey). Aliased
// so cli code — and the tests around it — keep reading `ConfigKey`.
type (
	SourceOption = provider.SourceOption
	ConfigKey    = provider.ConfigKey
	ConfigGroup  = provider.ConfigGroup
)

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

// providerKeysMarker is a placeholder ConfigKey naming the position in a
// group where the configured provider's own keys are spliced in (see
// schemaGroups). It never reaches a prompt or a config file — the splice
// replaces it, and a group whose provider can't be determined drops it.
//
// The marker exists so key ORDER stays visible and authoritative here:
// prompts, the init form, and `config check` all walk a group's keys in
// order, and appending provider keys at the end would silently reshuffle
// every one of those surfaces.
const providerKeysMarker = "__provider_keys__"

// providerKeys is the marker entry, written out at each splice point.
var providerKeys = ConfigKey{Key: providerKeysMarker}

// configSchema is the central registry of config keys bosun itself
// owns. Read it through schemaGroups()/lookupGroup(), never directly:
// provider-specific keys (Jira's base URL, Slack's auth mode) belong to
// the provider packages and are spliced in at the providerKeys marker.
//
// Keys are resolved via fullKey(groupName, ck) → "groupName.ck.Key".
// Status mappings live under "issue_tracker.statuses" as a sub-group.
var configSchema = map[string]ConfigGroup{
	issue.ConfigGroup: {
		Label: "issue tracker",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider", Required: true},
			providerKeys,
			{Key: "project", Label: "project key", Example: "PROJ"},
			{Key: "board_id", Label: "board ID", Example: "123"},
		},
	},
	issue.ConfigGroup + ".statuses": {
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
			{Key: "root", Label: "workspace root", Example: ".workspaces"},
			// No Default: unset means "use the configured tracker's own
			// key grammar" (issue.TrackerDescriptor.ParseIdentifier).
			// Setting it overrides that grammar — the escape hatch for a
			// key shape the provider doesn't recognize.
			{Key: "issue_pattern", Label: "issue pattern", Example: `([A-Z][A-Z0-9]+-\d+)`},
		},
	},
	code.ConfigGroup: {
		Label: "code host",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider", Required: true},
			providerKeys,
			{Key: "merge_method", Label: "PR merge method", Options: []string{"squash", "merge", "rebase"}, Default: "squash"},
		},
	},
	"pull_request": {
		Label: "pull request",

		Keys: []ConfigKey{
			// No Default: unset means "each repository's own default
			// branch", which is the right answer far more often than a
			// workspace-wide literal. Setting it makes it a global
			// override applied to every repo.
			{Key: "base", Label: "base branch", Example: "main"},
			{Key: "title_template", Label: "PR title template", Default: "[{{.IssueKey}}] {{.IssueTitle}}"},
			{Key: "body_template", Label: "PR body template"},
			{Key: "reviewers", Label: "reviewers (host usernames)"},
			{Key: "team_reviewers", Label: "team reviewers (host team slugs)"},
			{Key: "assignees", Label: "assignees (host usernames)"},
			{Key: "self_assign", Label: "auto-assign PR author", Default: "true"},
		},
	},
	notify.ConfigGroup: {
		Label: "notification",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider"},
			providerKeys,
			{Key: "channel_review", Label: "review channel", Example: "bb-prs"},
			{Key: "channel_prerelease", Label: "prerelease channel", Example: "release_coordination"},
		},
	},
	cicd.ConfigGroup: {
		Label: "CI/CD",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider"},
			providerKeys,
			{Key: "workflows.preview.url_template", Label: "preview URL template", Example: "https://host-ui-{{.Name}}.example.dev"},
			{Key: "workflows.preview.up.target", Label: "preview up workflow", Example: "org/repo/.github/workflows/deploy-preview.yml"},
			{Key: "workflows.preview.up.inputs.services", Label: "preview up services input", Default: "services-to-deploy"},
			{Key: "workflows.preview.up.inputs.name", Label: "preview up name input"},
			{Key: "workflows.preview.down.target", Label: "preview down workflow", Example: "org/repo/.github/workflows/teardown-preview.yml"},
			{Key: "workflows.preview.down.inputs.name", Label: "preview down name input"},
			{Key: "workflows.release.target", Label: "release production workflow(s)", Example: "per repo: a workflow path string, or a per-service {workflow, environment} map"},
			{Key: "workflows.release.inputs.version", Label: "release version input", Default: "version"},
		},
	},
	"display": {
		Label: "display",

		Keys: []ConfigKey{
			{Key: "color", Label: "color mode", Options: []string{"truecolor", "ansi", "none"}, Default: "truecolor"},
			{Key: "compact_header", Label: "compact header", Default: "false"},
		},
	},
}

// registerSource sets a Source function on a ConfigKey within a group.
// Called from init() to avoid package-level initialization cycles.
//
// This is the one write to configSchema, and therefore the one place
// that touches it directly rather than reading through schemaGroups —
// the resolved groups are rebuilt per call, so a Source set on one would
// evaporate. It follows that only keys bosun itself declares can carry a
// Source: a provider-contributed key named here would match nothing and
// silently no-op, leaving a picker as a free-text prompt. No provider
// declares one today; give provider.ConfigKey.Source a real home in the
// descriptor if one ever needs to.
//
// Safe without synchronization because every caller is an init()
// function, which runs before any goroutine exists. Every later access
// is a read.
func registerSource(group, key string, source func() ([]SourceOption, error)) {
	g := configSchema[group]
	for i := range g.Keys {
		if g.Keys[i].Key == key {
			g.Keys[i].Source = source
		}
	}
	configSchema[group] = g
}

// schemaGroups returns the effective config schema: bosun's own keys
// with each group's provider-contributed keys spliced in, and the
// "provider" key's selectable Options filled from the provider registry.
//
// Resolved per call rather than baked in at init because it depends on
// configuration — which provider a group is set to — and config isn't
// loaded when package vars initialize.
func schemaGroups() map[string]ConfigGroup {
	out := make(map[string]ConfigGroup, len(configSchema))
	for name, group := range configSchema {
		out[name] = resolveSchemaGroup(name, group)
	}
	return out
}

// resolveSchemaGroup expands one group: provider Options from the
// registry, provider keys spliced in at the marker.
func resolveSchemaGroup(name string, group ConfigGroup) ConfigGroup {
	names := services.ProviderNames(name)
	if names == nil {
		return group
	}

	keys := services.ProviderKeys(name, schemaProvider(name))

	out := group
	out.Keys = make([]ConfigKey, 0, len(group.Keys)+len(keys))
	for _, ck := range group.Keys {
		switch ck.Key {
		case providerKeysMarker:
			out.Keys = append(out.Keys, keys...)
		case "provider":
			ck.Options = names
			out.Keys = append(out.Keys, ck)
		default:
			out.Keys = append(out.Keys, ck)
		}
	}
	return out
}

// schemaProvider returns the provider whose keys a group should show:
// the configured one, or — when config hasn't said yet — the sole
// registered provider, since with one choice there is nothing to pick
// and hiding its keys would leave `bosun init` and `doctor` with nothing
// to ask for. With several registered and none chosen, no provider keys
// appear until the user picks one.
func schemaProvider(group string) string {
	if name := viper.GetString(group + ".provider"); name != "" {
		return name
	}
	return services.SoleProvider(group)
}

// secretKeys returns every fully-qualified config key that must never
// render in cleartext: bosun's own Secret keys plus those of EVERY
// registered provider, not only the configured one.
//
// Masking is the one schema read that has to fail closed, so it is the
// one read that doesn't go through schemaGroups. A group's provider keys
// are spliced in per the *configured* provider, so a provider value the
// registry doesn't recognize — a typo like "Jira", a provider name from
// a newer bosun, or any group left unset once a second provider is
// registered — drops that provider's token key out of the effective
// schema. Every "is this secret?" lookup would then answer no for a
// token sitting right there in the settings map, and `bosun config show`
// would print it. Taking the union costs one pass and removes the class.
func secretKeys() map[string]bool {
	out := make(map[string]bool)
	record := func(groupName string, ck ConfigKey) {
		if ck.Secret {
			out[fullKey(groupName, ck)] = true
		}
	}
	for groupName, group := range configSchema {
		for _, ck := range group.Keys {
			record(groupName, ck)
		}
		for _, name := range services.ProviderNames(groupName) {
			for _, ck := range services.ProviderKeys(groupName, name) {
				record(groupName, ck)
			}
		}
	}
	return out
}

// isSecretKey reports whether a fully-qualified config key holds a
// secret. Use it rather than a schema lookup's Secret field wherever the
// answer decides whether a value is printed — see secretKeys.
func isSecretKey(key string) bool { return secretKeys()[key] }

// lookupGroup returns the effective config group for a given name.
func lookupGroup(name string) (ConfigGroup, bool) {
	g, ok := configSchema[name]
	if !ok {
		return ConfigGroup{}, false
	}
	return resolveSchemaGroup(name, g), true
}

// fullKey returns the fully-qualified viper key for a group key.
// For nested groups like "issue_tracker", the key is prefixed:
// "issue_tracker.base_url". Top-level keys (where key equals group
// name) are returned as-is for backward compatibility.
func fullKey(groupName string, key ConfigKey) string {
	if key.Key == groupName || len(groupName) == 0 {
		return key.Key
	}
	return groupName + "." + key.Key
}
