package cli

import (
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/provider"
	"github.com/nickawilliams/bosun/internal/services"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/spf13/viper"
)

// The config-schema vocabulary is shared with the provider adapters,
// which contribute keys of their own (see provider.ConfigKey). Aliased
// so cli code — and the tests around it — keep reading `ConfigKey`.
type (
	SourceOption = provider.SourceOption
	ConfigKey    = provider.ConfigKey
	ConfigGroup  = provider.ConfigGroup
	Scope        = provider.Scope
)

// The config layers, aliased alongside the vocabulary above so a schema
// entry reads `Scope: ScopeAny` rather than naming the provider package
// on every line of a file that is nothing but schema.
const (
	ScopeGlobal  = provider.ScopeGlobal
	ScopeProject = provider.ScopeProject
	ScopeRepo    = provider.ScopeRepo
	ScopeCentral = provider.ScopeCentral
	ScopeAny     = provider.ScopeAny
)

// servicesConfigGroup is the config key prefix for the repo→service
// topology. A literal for the same reason uiConfigGroup is one: there
// is no `services` capability for the constant to live beside.
// internal/services is the provider registry, which is unrelated —
// naming this after it would be a coincidence, not a reference.
const servicesConfigGroup = "services"

// uiConfigGroup is the config key prefix for presentation settings.
// It is a literal rather than a ui.ConfigGroup constant because `ui` is
// not a registered capability yet — nothing selects a provider for it,
// so there is no descriptor for the constant to live beside.
//
// TODO(arch #83): register ui as a capability and move this constant.
const uiConfigGroup = "ui"

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
// One rule governs the shape: every top-level block is a capability,
// and a block earns root level only if that capability exists in code,
// registered or not. That admits `preview` (an interface with two
// adapters) and `ui` (internal/ui's Reporter with four
// implementations, merely unregistered), and excludes `release` —
// there is no internal/release, and a release deploy is literally a
// CI/CD workflow dispatch, so its keys stay under `cicd`.
//
// Sub-groups nest structurally (ConfigGroup.Groups) rather than through
// dotted Key strings. Keys are resolved via fullKey(groupName, ck) →
// "groupName.ck.Key", where groupName is the dotted path built by
// flattenSchemaGroup.
var configSchema = map[string]ConfigGroup{
	issue.ConfigGroup: {
		Label: "issue tracker",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider", Required: true},
			providerKeys,
			{Key: "project", Label: "project key", Example: "PROJ"},
			{Key: "board_id", Label: "board ID", Example: "123"},
			// No Default: unset means "use the configured tracker's own
			// key grammar" (issue.TrackerDescriptor.ParseIdentifier).
			// Setting it overrides that grammar — the escape hatch for a
			// key shape the provider doesn't recognize. It sits here,
			// not in `workspace`, because it overrides the *tracker's*
			// grammar; workspace names are only where it gets applied.
			//
			// NoPrompt because unset is the right answer for almost
			// every user, and this group is the one an adapter Requires
			// wholesale: prompting would put an unanswerable question in
			// front of every interactive command, and answering it with
			// the Example would pin one tracker's grammar over whichever
			// tracker is actually configured.
			{Key: "issue_pattern", Label: "issue pattern", Example: `([A-Z][A-Z0-9]+-\d+)`, NoPrompt: true},
		},

		Groups: []ConfigGroup{{
			Name:   "statuses",
			Label:  "status mappings",
			MapKey: "state",

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
		}},
	},
	code.ConfigGroup: {
		Label: "code host",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider", Required: true},
			providerKeys,
			{Key: "merge_method", Label: "PR merge method", Options: []string{"squash", "merge", "rebase"}, Default: "squash"},
		},

		// Only the host-wide half of PR configuration lives here. The
		// repo-scoped policy keys (base, reviewers, team_reviewers,
		// assignees) stay under `pull_request` because they are leaving
		// for per-repo descriptors (#82); relocating them now would
		// migrate the same keys twice.
		Groups: []ConfigGroup{{
			Name:  "pr",
			Label: "pull request",

			Keys: []ConfigKey{
				{Key: "title_template", Label: "PR title template", Default: "[{{.IssueKey}}] {{.IssueTitle}}"},
				{Key: "body_template", Label: "PR body template"},
				{Key: "self_assign", Label: "auto-assign PR author", Default: "true"},
			},
		}},
	},
	vcs.ConfigGroup: {
		Label: "version control",

		// No `provider` key: git is the only implementation and nothing
		// reads a selector for it. Declaring a key nothing reads is
		// worse than omitting it — `config check` would validate a
		// value that changes nothing.
		Groups: []ConfigGroup{{
			Name:  "branch",
			Label: "branch naming",

			// One template, deliberately: the branch name and the
			// workspace directory segment are the same string. The slug
			// is user-entered and persisted only in the name, so
			// splitting them makes it unrecoverable on resume. A future
			// workspace.name_template is additive.
			Keys: []ConfigKey{
				{Key: "template", Label: "branch template", Default: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"},
			},

			Groups: []ConfigGroup{{
				Name:   "categories",
				Label:  "branch categories",
				MapKey: "issue type",

				Keys: []ConfigKey{
					{Key: "story", Label: "story category", Default: "feature"},
					{Key: "bug", Label: "bug category", Default: "fix"},
					{Key: "task", Label: "task category", Default: "chore"},
				},
			}},
		}},
	},
	"workspace": {
		Label: "workspace",

		Keys: []ConfigKey{
			{Key: "root", Label: "workspace root", Example: ".workspaces"},
			// Central rather than per-repo, and it has to be: the globs
			// are how bosun finds the repositories in the first place,
			// so they cannot live in the files it finds.
			{Key: "repositories", Label: "repository globs", Example: "./*"},
		},
	},
	"pull_request": {
		Label: "pull request",

		// Repo-scoped policy: every key here answers a question about
		// ONE repository, so every key is ScopeAny — settable centrally
		// for repositories with no descriptor of their own, and
		// overridable by any repository that has one. Everything
		// host-wide moved to code_host.pr.
		//
		// This is the block per-repo descriptors were built for. Held
		// centrally, `reviewers` was resolved once for a whole
		// multi-repo fan-out and applied to every PR in it, so a team
		// that owns one repository was requested on all of them, with
		// no mechanism to vary it.
		Keys: []ConfigKey{
			// No Default: unset means "each repository's own default
			// branch", which is the right answer far more often than a
			// workspace-wide literal. Setting it centrally makes it an
			// override applied to every repo that doesn't override back.
			{Key: "base", Label: "base branch", Example: "main", Scope: ScopeAny},
			{Key: "reviewers", Label: "reviewers (host usernames)", Scope: ScopeAny},
			{Key: "team_reviewers", Label: "team reviewers (host team slugs)", Scope: ScopeAny},
			{Key: "assignees", Label: "assignees (host usernames)", Scope: ScopeAny},
		},
	},
	servicesConfigGroup: {
		Label: "services",

		// The repo→service topology: which deployable services each
		// repository contributes, and optionally which paths belong to
		// each. It is the one block whose two layers hold different
		// SHAPES, so it declares both halves.
		//
		// Centrally it is a map keyed by repository name, and MapScope
		// is what says only the central layers may write that map — a
		// repository naming another repository's services is authority
		// a committed file must not have.
		MapKey:   "repository",
		MapScope: ScopeCentral,

		// The descriptor half: a bare `services` whose value is exactly
		// what would have sat under the repository's key centrally (a
		// name, a list of names, or a map of name → path prefixes).
		// fullKey collapses a key whose name equals its group's to the
		// group name itself, so this declares the top-level `services`
		// key and nothing deeper.
		Keys: []ConfigKey{
			{
				Key:      servicesConfigGroup,
				Label:    "services",
				Example:  "a service name, a list of them, or a map of name → path prefixes",
				Scope:    ScopeRepo,
				NoPrompt: true,
			},
		},
	},
	notify.ConfigGroup: {
		Label: "notification",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider"},
			providerKeys,
		},

		Groups: []ConfigGroup{
			{
				// Keyed by notification *type*, not lifecycle stage:
				// buildNotifyContent takes a notifType, and preview.go
				// passes "review" — three stages, two types.
				Name:  "channels",
				Label: "notification channels",

				Keys: []ConfigKey{
					{Key: "review", Label: "review channel", Example: "bb-prs"},
					{Key: "prerelease", Label: "prerelease channel", Example: "release_coordination"},
				},
			},
			{
				// Read straight from viper by buildNotifyContent, in
				// either of two shapes: a string (plain text) or a map
				// of header/body/context overrides. Declared here so
				// the unknown-key check knows the block exists; the
				// per-type contents are the user's to name.
				Name:   "templates",
				Label:  "notification templates",
				MapKey: "type",
			},
		},
	},
	cicd.ConfigGroup: {
		Label: "CI/CD",

		Keys: []ConfigKey{
			{Key: "provider", Label: "provider"},
			providerKeys,
		},

		// Only release remains under `workflows` now that preview has
		// its own block. The level stays: it is meaningful against
		// cicd.provider, and a future stage that dispatches a workflow
		// slots in beside release.
		Groups: []ConfigGroup{{
			Name:  "workflows",
			Label: "workflows",

			Groups: []ConfigGroup{{
				Name:  "release",
				Label: "release workflow",

				// ScopeAny, and the two layers hold different shapes for
				// the same reason `services` does: centrally this is a
				// map keyed by repository name, while a descriptor sets
				// the value that would have sat under its own key — a
				// workflow path, or a per-service map. The bare central
				// scalar (one workflow for the whole workspace) is a
				// third shape, and stays central-only.
				Keys: []ConfigKey{
					{Key: "target", Label: "release production workflow(s)", Example: "a workflow path string, or a per-service {workflow, environment} map", Scope: ScopeAny},
				},

				Groups: []ConfigGroup{{
					Name:   "inputs",
					Label:  "release workflow inputs",
					MapKey: "concept",

					Keys: []ConfigKey{
						{Key: "version", Label: "release version input", Default: "version"},
					},
				}},
			}},
		}},
	},
	preview.ConfigGroup: {
		Label: "preview",

		// A capability with its own block: preview.Provider has two
		// adapters, and only one of them dispatches workflows. Leaving
		// these keys under `cicd` described the ephemeral adapter as
		// CI/CD configuration, which it is not.
		Keys: []ConfigKey{
			{Key: "provider", Label: "provider"},
			providerKeys,
			{Key: "url_template", Label: "preview URL template", Example: "https://host-ui-{{.Name}}.example.dev"},
		},

		Groups: []ConfigGroup{
			previewStageGroup("up", "preview up", "org/repo/.github/workflows/deploy-preview.yml"),
			previewStageGroup("down", "preview down", "org/repo/.github/workflows/teardown-preview.yml"),
		},
	},
	uiConfigGroup: {
		Label: "UI",

		// No `provider` key: internal/ui picks its Reporter at runtime
		// rather than from config, so a selector would be inert.
		//
		// TODO(arch #83): color and compact_header belong to a `stdio`
		// provider, not to the capability itself.
		Keys: []ConfigKey{
			{Key: "color", Label: "color mode", Options: []string{"truecolor", "ansi", "none"}, Default: "truecolor"},
			{Key: "compact_header", Label: "compact header", Default: "false"},
		},
	},
}

// previewStageGroup builds the up/down sub-group, which is the same
// shape either way: one workflow target plus the input-name mappings
// the cicd adapter passes to it.
//
// There is no `services` input. Deploying a subset of services leaves
// the environment half-built, so a filter bosun can set is a footgun
// rather than a feature — the key is gone, not renamed.
func previewStageGroup(name, label, workflowExample string) ConfigGroup {
	return ConfigGroup{
		Name:  name,
		Label: label,

		Keys: []ConfigKey{
			{Key: "workflow", Label: label + " workflow", Example: workflowExample},
		},

		Groups: []ConfigGroup{{
			Name:   "inputs",
			Label:  label + " inputs",
			MapKey: "concept",

			Keys: []ConfigKey{
				{Key: "name", Label: label + " name input"},
				{Key: "issue", Label: label + " issue input"},
			},
		}},
	}
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
// group is the dotted path, so a sub-group key is reachable
// ("issue_tracker.statuses"); the walk descends through Groups to find
// it. Safe without synchronization because every caller is an init()
// function, which runs before any goroutine exists. Every later access
// is a read.
func registerSource(group, key string, source func() ([]SourceOption, error)) {
	root, rest, _ := strings.Cut(group, ".")
	g, ok := configSchema[root]
	if !ok {
		return
	}
	if setGroupSource(&g, rest, key, source) {
		configSchema[root] = g
	}
}

// setGroupSource walks path (dot-separated, relative to g) and sets
// source on the named key. Reports whether it found the key.
func setGroupSource(g *ConfigGroup, path, key string, source func() ([]SourceOption, error)) bool {
	if path != "" {
		segment, rest, _ := strings.Cut(path, ".")
		for i := range g.Groups {
			if g.Groups[i].Name == segment {
				return setGroupSource(&g.Groups[i], rest, key, source)
			}
		}
		return false
	}
	for i := range g.Keys {
		if g.Keys[i].Key == key {
			g.Keys[i].Source = source
			return true
		}
	}
	return false
}

// schemaGroups returns the effective config schema, flattened: every
// group and sub-group keyed by its dotted path, with each top-level
// group's provider-contributed keys spliced in and the "provider" key's
// selectable Options filled from the provider registry.
//
// Flattening is what keeps the consumers simple — `config check`,
// `bosun init`, requireConfig and the source-attribution tree all want
// "a group and its keys", not a tree walk. The nesting lives in the
// declaration, where it makes the shape of the YAML legible; the
// resolved view is flat, where it makes the consumers legible.
//
// Resolved per call rather than baked in at init because it depends on
// configuration — which provider a group is set to — and config isn't
// loaded when package vars initialize.
func schemaGroups() map[string]ConfigGroup {
	out := make(map[string]ConfigGroup, len(configSchema))
	for name, group := range configSchema {
		flattenSchemaGroup(name, resolveSchemaGroup(name, group), out)
	}
	return out
}

// flattenSchemaGroup writes group into out under path and recurses into
// its sub-groups, whose paths are path + "." + Name. The written groups
// keep their Groups field so a caller that wants the tree still has it.
func flattenSchemaGroup(path string, group ConfigGroup, out map[string]ConfigGroup) {
	out[path] = group
	for _, sub := range group.Groups {
		flattenSchemaGroup(path+"."+sub.Name, sub, out)
	}
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
			// Show what an unset key resolves to. Only a capability that
			// declares a default has one — with several providers and no
			// default there is nothing honest to prefill, and with a
			// single provider the registry reports it either way.
			ck.Default = services.DefaultProvider(name)
			out.Keys = append(out.Keys, ck)
		default:
			out.Keys = append(out.Keys, ck)
		}
	}
	return out
}

// schemaProvider returns the provider whose keys a group should show:
// the configured one, or — when config hasn't said yet — whichever one
// an unset key resolves to, since hiding its keys would leave `bosun
// init` and `doctor` with nothing to ask for. With several registered,
// none chosen, and no declared default, no provider keys appear until
// the user picks one.
//
// This deliberately asks the same question the construction path asks
// (services.DefaultProvider, which covers both the sole-provider and
// declared-default cases). Falling back to the sole provider here while
// the registry falls back to a declared default would show one
// provider's keys and then build a different provider.
func schemaProvider(group string) string {
	if name := viper.GetString(group + ".provider"); name != "" {
		return name
	}
	return services.DefaultProvider(group)
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
	// Unresolved: the declared tree, so sub-group keys are reached
	// without depending on which provider is configured.
	declared := make(map[string]ConfigGroup)
	for name, group := range configSchema {
		flattenSchemaGroup(name, group, declared)
	}
	for groupName, group := range declared {
		for _, ck := range group.Keys {
			record(groupName, ck)
		}
	}
	for groupName := range configSchema {
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

// lookupGroup returns the effective config group for a given name,
// which may be a top-level block ("preview") or a dotted sub-group
// path ("preview.up.inputs").
func lookupGroup(name string) (ConfigGroup, bool) {
	root, _, _ := strings.Cut(name, ".")
	g, ok := configSchema[root]
	if !ok {
		return ConfigGroup{}, false
	}
	flat := make(map[string]ConfigGroup)
	flattenSchemaGroup(root, resolveSchemaGroup(root, g), flat)
	out, ok := flat[name]
	return out, ok
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
