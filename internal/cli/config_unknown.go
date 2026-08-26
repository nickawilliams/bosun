package cli

import (
	"sort"
	"strings"

	"github.com/nickawilliams/bosun/internal/services"
	"github.com/spf13/viper"
)

// unknownKeyExempt lists top-level blocks the unknown-key walk skips
// wholesale, because they are real config the schema deliberately does
// not describe.
//
// `services.<repo>` is the only member: it is repo-scoped and leaves
// for the per-repo descriptor in #82. Exempting it is cheaper than
// declaring it map-shaped and then deleting the declaration one issue
// later, and either way the walk must not report a key that
// resolveRepoServiceNames reads and acts on.
//
// TODO(arch #82): drop the exemption when services moves to .bosun.yaml.
var unknownKeyExempt = map[string]bool{
	"services": true,
}

// unknownConfigKeys returns the fully-qualified keys present in the
// merged config that the effective schema does not account for, sorted.
//
// It is the inverse of validateGroup: that walks the schema and asks
// what config is missing, this walks the config and asks what the
// schema doesn't recognize. Both are needed — a rename is invisible to
// the first (the new key merely looks unset, which for an optional key
// is silent) and obvious to the second.
//
// A key is accounted for when it is declared, sits beneath a declared
// key (cicd.workflows.release.target takes a per-repo map, so its own
// subtree is its business), sits beneath a map-shaped group, or belongs
// to an exempt block. Everything else is reported.
//
// That third rule is deliberately looser than "beneath a key that takes
// a map": nothing marks which keys those are, so it admits
// `ui.color.foo` too. Narrowing it would mean a per-key annotation, and
// the failure modes are not symmetric — a forgotten annotation reports
// working config as broken, while the loose rule only misses a typo
// nested under a scalar key. It fails safe in the direction that
// doesn't cost the user a debugging session.
func unknownConfigKeys(groupFilter string) []string {
	declared, mapPrefixes := schemaKeySurface()

	var out []string
	for _, key := range viper.AllKeys() {
		if !keyInFilter(key, groupFilter) {
			continue
		}
		if unknownKeyExempt[topLevelSegment(key)] {
			continue
		}
		if declared[key] || hasPrefixIn(key, declared) || hasPrefixIn(key, mapPrefixes) {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// schemaKeySurface returns everything the schema recognizes: the set of
// fully-qualified declared keys, and the set of map-shaped group paths
// beneath which any key is permitted.
//
// Provider keys are taken as the union across EVERY registered
// provider, not just the configured one — the same reasoning secretKeys
// documents. Splicing only the configured provider's keys would report
// the other provider's keys as unknown, so switching preview.provider
// from `cicd` to `ephemeral` and back would make a working config look
// broken in the half of the round trip where the keys are still there.
func schemaKeySurface() (declared, mapPrefixes map[string]bool) {
	declared = make(map[string]bool)
	mapPrefixes = make(map[string]bool)

	for groupName, group := range schemaGroups() {
		if group.MapKey != "" {
			mapPrefixes[groupName] = true
		}
		for _, ck := range group.Keys {
			if ck.Key == providerKeysMarker {
				// A group whose capability has no registered provider
				// keeps the unspliced marker. It names no config key.
				continue
			}
			declared[fullKey(groupName, ck)] = true
		}
	}

	for groupName := range configSchema {
		for _, name := range services.ProviderNames(groupName) {
			for _, ck := range services.ProviderKeys(groupName, name) {
				declared[fullKey(groupName, ck)] = true
			}
		}
	}

	return declared, mapPrefixes
}

// keyInFilter reports whether key falls under the `config check [group]`
// filter. An empty filter admits everything; otherwise the key must be
// the group or sit beneath it.
func keyInFilter(key, groupFilter string) bool {
	if groupFilter == "" {
		return true
	}
	return key == groupFilter || strings.HasPrefix(key, groupFilter+".")
}

// topLevelSegment returns the first dot-separated segment of a key.
func topLevelSegment(key string) string {
	head, _, _ := strings.Cut(key, ".")
	return head
}

// hasPrefixIn reports whether any member of prefixes is a dotted
// ancestor of key.
func hasPrefixIn(key string, prefixes map[string]bool) bool {
	for p := range prefixes {
		if strings.HasPrefix(key, p+".") {
			return true
		}
	}
	return false
}
