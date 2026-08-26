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
// Empty since `services` was declared for real: it is map-shaped
// centrally and a plain key in a repo descriptor, and the schema now
// says so, which is strictly better than an exemption because it also
// says which layer may write which half. The map is kept rather than
// deleted so a future block that genuinely cannot be described has a
// place to go.
var unknownKeyExempt = map[string]bool{}

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
		// No providerKeysMarker guard: schemaGroups resolves the marker
		// away, and TestSchemaGroupsResolvesEveryGroup fails if one ever
		// survives into a group anyone walks. Guarding here as well
		// would put the invariant in two places and leave the weaker
		// copy — a marker admitted into `declared` names no real key
		// and hides nothing — permanently unexercised.
		for _, ck := range group.Keys {
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

// schemaScopeSurface returns the two scope tables the layer walk reads:
// every declared key's Scope, and every map-shaped group's MapScope.
//
// Provider keys are folded in as the union across every registered
// provider, matching schemaKeySurface — a scope answer that depended on
// which provider happened to be configured would report a working
// config as misplaced in half of a provider round trip.
func schemaScopeSurface() (keyScope, mapScope map[string]Scope) {
	keyScope = make(map[string]Scope)
	mapScope = make(map[string]Scope)

	for groupName, group := range schemaGroups() {
		if group.MapKey != "" {
			mapScope[groupName] = group.MapScope
		}
		for _, ck := range group.Keys {
			keyScope[fullKey(groupName, ck)] = ck.Scope
		}
	}

	for groupName := range configSchema {
		for _, name := range services.ProviderNames(groupName) {
			for _, ck := range services.ProviderKeys(groupName, name) {
				keyScope[fullKey(groupName, ck)] = ck.Scope
			}
		}
	}

	return keyScope, mapScope
}

// scopeForKey returns the Scope governing a fully-qualified key, and
// whether the schema accounts for the key at all. An unaccounted-for key
// is not a scope problem — it is an unknown key, which the caller
// reports separately and with a different message.
//
// Resolution mirrors unknownConfigKeys' notion of "accounted for": an
// exact declaration governs its own path, and otherwise the LONGEST
// declared ancestor governs — a map-shaped group winning a tie against a
// key of the same path.
//
// That tie is not hypothetical, and `services` is why. It is declared
// both ways at once: as a map-shaped group (the central
// `services.<repo>` form, central-only) and as a plain key of the same
// name (the descriptor form, repo-only). Anything BENEATH the path is
// the central map and must answer central; the path ITSELF is the
// descriptor key and must answer repo. Exact-match-first plus
// map-wins-ties is exactly that split, and getting it backwards would
// report every project's `services.<repo>` block as misplaced.
func scopeForKey(key string, keyScope, mapScope map[string]Scope) (Scope, bool) {
	if s, ok := keyScope[key]; ok {
		return s.Effective(), true
	}

	best := -1
	var found Scope
	var foundIsMap bool

	consider := func(prefix string, s Scope, isMap bool) {
		if !strings.HasPrefix(key, prefix+".") {
			return
		}
		// Longer prefix wins; on equal length the map-shaped group does.
		if len(prefix) > best || (len(prefix) == best && isMap && !foundIsMap) {
			best, found, foundIsMap = len(prefix), s, isMap
		}
	}
	for p, s := range mapScope {
		consider(p, s, true)
	}
	for p, s := range keyScope {
		consider(p, s, false)
	}

	return found.Effective(), best >= 0
}
