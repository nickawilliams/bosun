package cli

import (
	"fmt"
	"sort"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/provider"
)

// configLayer is one config file as the scope walk sees it: which layer
// it is, what to call it in a report, and the keys it sets.
//
// The walk is per FILE rather than over viper's merged view because
// scope is a property of where a value was written, and merging is
// precisely the operation that forgets that. The merged walk
// (unknownConfigKeys) answers a different question — "does the schema
// know this key at all?" — for which the merge is fine.
type configLayer struct {
	scope provider.Scope
	label string // "global config", "project config", "api/.bosun.yaml"
	keys  []string
}

// scopeIssue is one key set in a layer the schema does not permit it in,
// or set in a repo descriptor the schema does not recognize at all.
type scopeIssue struct {
	Key    string
	Detail string
}

// configLayers collects every config file bosun would read, in
// outermost-first order: the global file, the project file, then one
// per repository that carries a descriptor.
//
// Repositories come from resolveRepositories — the main checkouts, not
// the workspace's worktrees. `bosun config check` validates the project
// as configured rather than a piece of work in flight, and it takes no
// --workspace, so there is no branch for it to prefer. A resolution
// failure (no repository globs, or a run outside a project) is not an
// error here: it means there are no descriptors to reach, and the
// central layers still have plenty to say.
func configLayers(cs *configSources) []configLayer {
	var layers []configLayer

	if cs.global != nil {
		layers = append(layers, configLayer{
			scope: provider.ScopeGlobal,
			label: "global config",
			keys:  cs.global.AllKeys(),
		})
	}
	if cs.project != nil {
		layers = append(layers, configLayer{
			scope: provider.ScopeProject,
			label: "project config",
			keys:  cs.project.AllKeys(),
		})
	}

	repos, err := resolveRepositories(nil)
	if err != nil {
		return layers
	}
	for _, r := range repos {
		// Through loadRepoConfig rather than config.LoadRepoConfig so
		// an unreadable descriptor gets the same one-per-run warning
		// here that it gets from the commands that consume it. A
		// validation command staying quiet about a file it could not
		// read is the one outcome that would actively mislead.
		rc := loadRepoConfig(r)
		if rc.repo == nil {
			continue
		}
		layers = append(layers, configLayer{
			scope: provider.ScopeRepo,
			label: r.Name + "/" + config.RepoConfigFile,
			keys:  rc.repo.AllKeys(),
		})
	}

	return layers
}

// descriptorCount returns how many of layers are repo descriptors.
func descriptorCount(layers []configLayer) int {
	n := 0
	for _, l := range layers {
		if l.scope == provider.ScopeRepo {
			n++
		}
	}
	return n
}

// misplacedConfigKeys returns the keys each layer sets that the schema
// does not permit that layer to set, sorted by key within layer order.
//
// It is the third of `config check`'s three questions. validateGroup
// asks what the config is MISSING; unknownConfigKeys asks what the
// schema does not RECOGNIZE; this asks whether what it recognizes sits
// in a file allowed to hold it. The three are independent — a
// credential committed to a shared repository is present, recognized,
// and wrong.
//
// Repo descriptors are also checked for keys the schema doesn't know,
// which nothing else covers: descriptors are never merged into the
// global viper, so unknownConfigKeys cannot see them.
func misplacedConfigKeys(layers []configLayer, groupFilter string) []scopeIssue {
	keyScope, mapScope := schemaScopeSurface()

	var out []scopeIssue
	for _, layer := range layers {
		var issues []scopeIssue
		for _, key := range layer.keys {
			if !keyInFilter(key, groupFilter) || unknownKeyExempt[topLevelSegment(key)] {
				continue
			}

			scope, known := scopeForKey(key, keyScope, mapScope)
			switch {
			case !known:
				// Only descriptors report this here; the central layers
				// reach unknownConfigKeys through the merged view, and
				// saying it twice would double-count the warning.
				if layer.scope == provider.ScopeRepo {
					issues = append(issues, scopeIssue{
						Key:    key,
						Detail: fmt.Sprintf("not in schema (%s)", layer.label),
					})
				}
			case !scope.Allows(layer.scope):
				issues = append(issues, scopeIssue{
					Key:    key,
					Detail: fmt.Sprintf("not allowed in %s", layer.label),
				})
			}
		}
		sort.Slice(issues, func(i, j int) bool { return issues[i].Key < issues[j].Key })
		out = append(out, issues...)
	}
	return out
}
