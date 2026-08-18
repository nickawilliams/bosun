package cli

import (
	"slices"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
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

	want := []string{"provider", "base_url", "email", "token", "project", "board_id"}
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
	want := []string{"provider", "project", "board_id"}
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

// TestSchemaGroupsResolvesEveryGroup guards the accessor the whole
// config surface reads through: a group that schemaGroups drops (or
// leaves a marker in) would go missing from `config check`, the env-var
// scan, and secret masking all at once.
func TestSchemaGroupsResolvesEveryGroup(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	groups := schemaGroups()
	if len(groups) != len(configSchema) {
		t.Errorf("schemaGroups has %d groups, want %d", len(groups), len(configSchema))
	}
	for name, g := range groups {
		if slices.Contains(keyNames(g), providerKeysMarker) {
			t.Errorf("group %q still carries the provider-keys marker", name)
		}
	}
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

	for groupName, group := range configSchema {
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
