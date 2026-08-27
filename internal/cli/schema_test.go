package cli

import (
	"slices"
	"strings"
	"testing"

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

// keyNames returns a group's keys in order — the order prompts, the init
// form, and `config check` all walk.
func keyNames(g ConfigGroup) []string {
	out := make([]string, len(g.Keys))
	for i, ck := range g.Keys {
		out[i] = ck.Key
	}
	return out
}

// TestLookupGroupSplicesProviderKeys pins where a provider's keys land.
// Order is the contract: every surface that walks a group renders in key
// order, so appending the provider's keys instead of splicing them at the
// marker would silently reshuffle the init form and `config check`.
func TestLookupGroupSplicesProviderKeys(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	viper.Set(issue.ConfigGroup+".provider", "jira")

	group, ok := lookupGroup(issue.ConfigGroup)
	if !ok {
		t.Fatalf("lookupGroup(%q) not found", issue.ConfigGroup)
	}

	want := []string{"provider", "base_url", "email", "token", "project", "board_id", "issue_pattern"}
	if got := keyNames(group); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}

	// The marker itself must never survive into a group anyone walks.
	if slices.Contains(keyNames(group), providerKeysMarker) {
		t.Error("the provider-keys marker leaked into the resolved group")
	}
}

// TestLookupGroupFillsProviderOptions pins that the selectable providers
// come from the registry. A provider registered in services must show up
// in the init gate's select with no schema edit — that inversion is the
// whole point of the refactor.
func TestLookupGroupFillsProviderOptions(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	for _, group := range []string{
		issue.ConfigGroup, code.ConfigGroup, notify.ConfigGroup, cicd.ConfigGroup,
	} {
		t.Run(group, func(t *testing.T) {
			resolved, ok := lookupGroup(group)
			if !ok {
				t.Fatalf("lookupGroup(%q) not found", group)
			}
			ck, found := findGroupProviderKey(resolved)
			if !found {
				t.Fatalf("%s has no provider key", group)
			}
			if len(ck.Options) == 0 {
				t.Errorf("%s provider Options are empty — the registry didn't fill them", group)
			}
		})
	}
}

// TestLookupGroupProviderKeysFollowTheConfiguredProvider pins that the
// spliced keys track configuration rather than being a static union: with
// an unregistered provider named, no provider keys appear, so nothing
// prompts for another provider's fields.
func TestLookupGroupProviderKeysFollowTheConfiguredProvider(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	viper.Set(issue.ConfigGroup+".provider", "linear")

	group, _ := lookupGroup(issue.ConfigGroup)
	want := []string{"provider", "project", "board_id", "issue_pattern"}
	if got := keyNames(group); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v — only bosun's own keys for an unknown provider", got, want)
	}
}

// TestLookupGroupUnsetProviderUsesTheSoleOne covers the case that makes
// `bosun init` work on a fresh project: nothing configured yet, one
// provider registered, so its keys are already in the group and the
// wizard has something to ask for.
func TestLookupGroupUnsetProviderUsesTheSoleOne(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	group, _ := lookupGroup(code.ConfigGroup)
	if !slices.Contains(keyNames(group), "token") {
		t.Errorf("keys = %v, want the sole host's token key spliced in", keyNames(group))
	}
}

// declaredGroupPaths walks the declared schema and returns every group's
// dotted path — the paths schemaGroups is expected to produce.
func declaredGroupPaths(path string, g ConfigGroup, out *[]string) {
	*out = append(*out, path)
	for _, sub := range g.Groups {
		declaredGroupPaths(path+"."+sub.Name, sub, out)
	}
}

// TestSchemaGroupsResolvesEveryGroup guards the accessor the whole
// config surface reads through: a group that schemaGroups drops (or
// leaves a marker in) would go missing from `config check`, the env-var
// scan, and secret masking all at once.
//
// Sub-groups count. The schema declares nesting structurally and
// schemaGroups flattens it to dotted paths, so a flattening bug that
// stopped at the top level would take `issue_tracker.statuses` and
// every preview sub-stage down with it, silently.
func TestSchemaGroupsResolvesEveryGroup(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	var want []string
	for name, g := range configSchema {
		declaredGroupPaths(name, g, &want)
	}

	groups := schemaGroups()
	if len(groups) != len(want) {
		t.Errorf("schemaGroups has %d groups, want %d", len(groups), len(want))
	}
	for _, path := range want {
		if _, ok := groups[path]; !ok {
			t.Errorf("schemaGroups dropped %q", path)
		}
	}
	for name, g := range groups {
		if slices.Contains(keyNames(g), providerKeysMarker) {
			t.Errorf("group %q still carries the provider-keys marker", name)
		}
	}
}

// TestIsKnownConfigGroup covers `config show <group>`'s validation. The
// schema arm exists for groups that are entirely optional — every key
// example-only — which inject nothing and so appear in no settings map
// until the user sets something; rejecting those would report a real
// group as unknown. Sub-groups are excluded because the filter is matched
// against top-level tree keys, so accepting one would render nothing and
// call it success.
func TestIsKnownConfigGroup(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	tests := []struct {
		name string
		want bool
	}{
		{"workspace", true},               // schema-known, injects nothing
		{"issue_tracker", true},           // schema-known
		{"ui", true},                      // schema-known, has defaults
		{"issue_tracker.statuses", false}, // a sub-group, not top-level
		{"nonsense_group", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isKnownConfigGroup(tt.name); got != tt.want {
				t.Errorf("isKnownConfigGroup(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// TestSecretKeysAreProviderIndependent is the regression guard for a
// leak the splice would otherwise open: provider keys are spliced in per
// the CONFIGURED provider, so a provider value the registry doesn't know
// drops that provider's token key from the effective schema — and every
// "is this secret?" lookup answers no for a token sitting in the config.
// Masking has to fail closed, so it reads the union of every registered
// provider's keys instead.
func TestSecretKeysAreProviderIndependent(t *testing.T) {
	t.Cleanup(viper.Reset)

	tokens := []string{
		issue.ConfigGroup + ".token",
		code.ConfigGroup + ".token",
		notify.ConfigGroup + ".token",
	}

	// A typo'd provider, a provider from a newer bosun, and no provider
	// at all: none of them may unmask a token.
	for _, name := range []string{"jira", "Jira", "linear", ""} {
		t.Run("provider="+name, func(t *testing.T) {
			viper.Reset()
			if name != "" {
				for _, group := range []string{issue.ConfigGroup, code.ConfigGroup, notify.ConfigGroup} {
					viper.Set(group+".provider", name)
				}
			}
			for _, key := range tokens {
				if !isSecretKey(key) {
					t.Errorf("isSecretKey(%q) = false — the token would print in cleartext", key)
				}
			}
		})
	}

	t.Run("non-secret keys stay unmasked", func(t *testing.T) {
		viper.Reset()
		for _, key := range []string{
			issue.ConfigGroup + ".project",
			issue.ConfigGroup + ".base_url",
			notify.ConfigGroup + ".channels.review",
			"workspace.root",
		} {
			if isSecretKey(key) {
				t.Errorf("isSecretKey(%q) = true — masking a value that isn't a secret", key)
			}
		}
	})
}

// TestResolveSchemaGroupPreservesSources pins that the splice carries a
// key's registered Source through. Sources are attached by init() to the
// stored schema, and resolveSchemaGroup rebuilds the key slice on every
// lookup — dropping the field would silently turn the board picker into
// a free-text prompt, which reads as working.
func TestResolveSchemaGroupPreservesSources(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	viper.Set(issue.ConfigGroup+".provider", "jira")

	group, _ := lookupGroup(issue.ConfigGroup)
	for _, ck := range group.Keys {
		if ck.Key == "board_id" {
			if ck.Source == nil {
				t.Error("board_id lost its registered Source through the splice")
			}
			return
		}
	}
	t.Error("board_id is missing from the resolved group")
}

// TestProviderHint pins the generated config template's provider comment:
// it comes from the registry, so a newly registered provider is documented
// without a template edit.
func TestProviderHint(t *testing.T) {
	if got := providerHint(issue.ConfigGroup); got == "" || got == "…" {
		t.Errorf("providerHint(issue_tracker) = %q, want the registered provider(s)", got)
	}
	if got := providerHint("workspace"); got != "…" {
		t.Errorf("providerHint for a group with no providers = %q, want the placeholder", got)
	}
}

// TestSchemaCarriesNoProviderSpecificKeys is the regression guard for the
// leak this refactor closed: bosun's own schema must not name a
// provider's config. Each string below appeared literally in schema.go
// before the provider packages took ownership.
func TestSchemaCarriesNoProviderSpecificKeys(t *testing.T) {
	forbidden := map[string]string{
		"base_url":  "Jira's site URL",
		"email":     "Jira's account",
		"auth":      "Slack's auth mode",
		"workspace": "Slack's workspace name",
	}

	// Sub-groups included: `preview` grew two of them, and a
	// provider-flavored key smuggled into `preview.up` is the same leak
	// one level down.
	declared := make(map[string]ConfigGroup)
	for name, group := range configSchema {
		flattenSchemaGroup(name, group, declared)
	}

	for groupName, group := range declared {
		for _, ck := range group.Keys {
			if why, bad := forbidden[ck.Key]; bad {
				t.Errorf("schema group %q declares %q (%s) — it belongs to the provider",
					groupName, ck.Key, why)
			}
			if ck.EnvVar == "BOSUN_JIRA_TOKEN" || ck.EnvVar == "BOSUN_SLACK_TOKEN" {
				t.Errorf("schema group %q hardcodes the provider env var %q",
					groupName, ck.EnvVar)
			}
			if len(ck.Options) > 0 && ck.Key == "provider" {
				t.Errorf("schema group %q hardcodes provider Options %v — the registry supplies them",
					groupName, ck.Options)
			}
		}
	}
}

// TestSchemaNestingIsStructural pins that sub-groups are declared as
// nested ConfigGroups rather than as dotted key strings. The flattened
// view is what every consumer reads, so the two have to agree: a key
// declared inside `statuses` must resolve to
// "issue_tracker.statuses.ready" and must not also appear as a dotted
// key on its parent.
func TestSchemaNestingIsStructural(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	groups := schemaGroups()

	sub, ok := groups[issue.ConfigGroup+".statuses"]
	if !ok {
		t.Fatalf("issue_tracker.statuses missing from the flattened schema")
	}
	if !slices.Contains(keyNames(sub), "ready") {
		t.Errorf("statuses keys = %v, want the bare key name", keyNames(sub))
	}
	if got := fullKey(issue.ConfigGroup+".statuses", sub.Keys[0]); got != "issue_tracker.statuses.ready" {
		t.Errorf("fullKey = %q, want issue_tracker.statuses.ready", got)
	}

	// No key anywhere may carry a dot: a dotted key is nesting smuggled
	// into a string, which is what the structural form replaced.
	for name, g := range groups {
		for _, ck := range g.Keys {
			if strings.Contains(ck.Key, ".") {
				t.Errorf("group %q declares dotted key %q — declare a sub-group instead", name, ck.Key)
			}
		}
	}
}

// TestRegisterSourceReachesSubGroups pins that registerSource addresses
// the same dotted namespace lookupGroup exposes. Only a top-level key
// registers a Source today; the sub-group path exists so that adding one
// (a status picker fed by the tracker's own states, say) doesn't
// silently no-op and leave a free-text prompt that reads as working.
func TestRegisterSourceReachesSubGroups(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	src := func() ([]SourceOption, error) { return nil, nil }
	registerSource(issue.ConfigGroup+".statuses", "ready", src)
	t.Cleanup(func() { registerSource(issue.ConfigGroup+".statuses", "ready", nil) })

	group, ok := lookupGroup(issue.ConfigGroup + ".statuses")
	if !ok {
		t.Fatal("issue_tracker.statuses not found")
	}
	for _, ck := range group.Keys {
		if ck.Key == "ready" {
			if ck.Source == nil {
				t.Error("registerSource did not reach the sub-group key")
			}
			return
		}
	}
	t.Error("ready is missing from the resolved sub-group")
}

// TestRegisterSourceIgnoresUnknownTargets pins that a misaddressed
// registration is inert rather than a panic: the callers are init()
// functions, so a nil-map write or an index into a missing group would
// take the whole binary down at startup.
func TestRegisterSourceIgnoresUnknownTargets(t *testing.T) {
	registerSource("nonsense_group", "key", func() ([]SourceOption, error) { return nil, nil })
	registerSource(issue.ConfigGroup+".nonsense_sub", "ready", func() ([]SourceOption, error) { return nil, nil })
	registerSource(issue.ConfigGroup, "nonsense_key", func() ([]SourceOption, error) { return nil, nil })
}

// TestCapabilityBlocksOnly pins the schema's organizing rule: every
// top-level block names a capability that exists in code. The two
// exceptions are named here rather than left implicit, so adding a third
// is a deliberate edit to this list and not an accident.
func TestCapabilityBlocksOnly(t *testing.T) {
	// Blocks that are not capabilities, and why they are tolerated.
	exceptions := map[string]string{
		"workspace":    "bosun's own concept — where worktrees live and which repos are in scope",
		"pull_request": "repo-scoped policy, settable centrally and overridable per repo",
		"services":     "the repo→service topology — a fact about the repos, not a capability bosun implements",
	}
	capabilities := []string{
		issue.ConfigGroup, code.ConfigGroup, notify.ConfigGroup,
		cicd.ConfigGroup, preview.ConfigGroup, vcs.ConfigGroup, uiConfigGroup,
	}

	for name := range configSchema {
		if _, ok := exceptions[name]; ok {
			continue
		}
		if !slices.Contains(capabilities, name) {
			t.Errorf("top-level block %q is neither a capability nor a listed exception", name)
		}
	}

	// The blocks the reshape refused. `release` has no internal/release
	// to be a provider for; `display` and `branch` were axes, not
	// capabilities; `repositories` was a bare key at root.
	for _, gone := range []string{"release", "display", "branch", "repositories"} {
		if _, ok := configSchema[gone]; ok {
			t.Errorf("%q is back as a top-level block", gone)
		}
	}
}

// TestMapShapedGroups pins which groups accept user-chosen key names.
// It is the declaration the unknown-key check reads, so a group that
// lost its MapKey would start reporting the user's own status names and
// notification types as typos.
func TestMapShapedGroups(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	want := []string{
		issue.ConfigGroup + ".statuses",
		vcs.ConfigGroup + ".branch.categories",
		notify.ConfigGroup + ".templates",
		preview.ConfigGroup + ".up.inputs",
		preview.ConfigGroup + ".down.inputs",
		cicd.ConfigGroup + ".workflows.release.inputs",
		servicesConfigGroup,
	}

	groups := schemaGroups()
	for _, name := range want {
		g, ok := groups[name]
		if !ok {
			t.Errorf("%q missing from the schema", name)
			continue
		}
		if g.MapKey == "" {
			t.Errorf("%q is not declared map-shaped", name)
		}
	}

	// services is declared for real now rather than exempted, which is
	// what lets the scope walk say which layer may write which of its
	// two forms. An exemption would have to skip both.
	if len(unknownKeyExempt) != 0 {
		t.Errorf("unknownKeyExempt = %v, want empty — blocks should be declared, not exempted", unknownKeyExempt)
	}
}

// TestSecretKeysAreNeverRepoScoped is the pairing ConfigKey.Scope's
// zero value was chosen to make automatic: a secret has no business in
// a file committed to a repository the whole team can read, and the
// default denies it without a rule of its own. This test is what turns
// "nobody would do that" into something that fails the build.
//
// Provider keys are included — GitHub's and Jira's tokens are declared
// in the provider packages, which is exactly where a Scope annotation
// would be added without anyone thinking about this file.
func TestSecretKeysAreNeverRepoScoped(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	check := func(groupName string, ck ConfigKey) {
		if ck.Secret && ck.Scope.Allows(provider.ScopeRepo) {
			t.Errorf("%s: secret key %q is repo-scoped — it would be committed to a shared repository",
				groupName, ck.Key)
		}
	}

	declared := make(map[string]ConfigGroup)
	for name, group := range configSchema {
		flattenSchemaGroup(name, group, declared)
	}
	for groupName, group := range declared {
		for _, ck := range group.Keys {
			check(groupName, ck)
		}
	}
	for groupName := range configSchema {
		for _, name := range services.ProviderNames(groupName) {
			for _, ck := range services.ProviderKeys(groupName, name) {
				check(groupName, ck)
			}
		}
	}
}

// TestRepoScopedKeys pins exactly which keys a repository may set in
// its own descriptor. The list is short on purpose: everything on it
// answers a question about ONE repository, and everything off it either
// describes the workspace (which repos exist, where worktrees go) or
// the user's own machine.
//
// Pinning it as a set rather than spot-checking members is what makes
// widening the repo surface a deliberate edit. A key that drifts to
// ScopeAny by copy-paste becomes writable by anyone with commit access
// to any repository bosun reads.
func TestRepoScopedKeys(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	want := []string{
		"cicd.workflows.release.target",
		"pull_request.assignees",
		"pull_request.base",
		"pull_request.reviewers",
		"pull_request.team_reviewers",
		servicesConfigGroup,
	}

	var got []string
	for groupName, group := range schemaGroups() {
		for _, ck := range group.Keys {
			if ck.Scope.Allows(provider.ScopeRepo) {
				got = append(got, fullKey(groupName, ck))
			}
		}
	}
	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Errorf("repo-scoped keys = %v, want %v", got, want)
	}
}

// TestUnknownConfigKeys covers the walk `bosun config check` runs
// against the merged config. It is the only thing that surfaces a key
// this reshape renamed: the new key merely looks unset, which for an
// optional key is silent.
func TestUnknownConfigKeys(t *testing.T) {
	known := []struct {
		key, why string
	}{
		{"issue_tracker.project", "a declared key"},
		{"issue_tracker.statuses.ready", "a declared key in a sub-group"},
		{"issue_tracker.statuses.triage", "a user-chosen key in a map-shaped group"},
		{"notification.templates.review.header", "two levels under a map-shaped group"},
		{"preview.up.inputs.name", "a declared key three levels down"},
		{"preview.base_url", "a provider key from an unselected provider"},
		{"cicd.workflows.release.target.my-repo", "beneath a declared map-valued key"},
		{"services.my-repo", "the exempted block"},
		{"workspace.repositories", "a moved key at its new home"},
	}
	for _, tc := range known {
		t.Run("known/"+tc.key, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tc.key, "x")
			if got := unknownConfigKeys(""); len(got) != 0 {
				t.Errorf("%s (%s) reported as unknown: %v", tc.key, tc.why, got)
			}
		})
	}

	unknown := []struct {
		key, why string
	}{
		{"pull_request.title_template", "moved to code_host.pr"},
		{"display.color", "moved to ui"},
		{"notification.channel_review", "moved to notification.channels"},
		{"branch.template", "moved to vcs.branch"},
		{"repositories", "moved to workspace.repositories"},
		{"cicd.workflows.preview.up.target", "moved to preview.up.workflow"},
		{"preview.api.base_url", "flattened to preview.base_url"},
		{"issue", "env-only, never a config key"},
		{"project", "env-only, never a config key"},
	}
	for _, tc := range unknown {
		t.Run("unknown/"+tc.key, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tc.key, "x")
			got := unknownConfigKeys("")
			if !slices.Contains(got, tc.key) {
				t.Errorf("%s (%s) not reported; got %v", tc.key, tc.why, got)
			}
		})
	}

	t.Run("filter narrows to one block", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		viper.Set("display.color", "x")
		viper.Set("branch.template", "y")

		if got := unknownConfigKeys("branch"); !slices.Equal(got, []string{"branch.template"}) {
			t.Errorf("filtered = %v, want only branch.template", got)
		}
	})
}
