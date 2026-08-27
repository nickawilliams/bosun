package cli

import (
	"sync"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// repoConfig is one repository's effective configuration: the merged
// global+project settings with that repository's own committed
// `.bosun.yaml` laid over the top. It is the read side of the third
// config layer.
//
// It exists as a small explicit type rather than a merge into the
// global viper because a lifecycle command fans out over several
// repositories at once. There is no single "current repository" a
// merged view could describe, and a merge would silently give every
// repository the last one's descriptor.
//
// The descriptor is read from whatever path the caller resolved, which
// is the point: resolveActiveRepositories hands back worktree paths, so
// a branch that adds a service or changes its reviewers is read from
// the branch. resolveRepositories hands back main checkouts, which is
// the right answer for the commands that use it (`status`, `doctor`) —
// they describe the project, not a piece of work in flight.
type repoConfig struct {
	// name is the repository's bosun name (its directory basename),
	// which is the key the central repo-keyed maps use.
	name string

	// repo holds the descriptor's settings, or nil when the repository
	// has none. Nil is the ordinary case and every read handles it by
	// falling through to the central layers.
	repo *viper.Viper
}

// warnedDescriptors records the descriptor paths already reported as
// unreadable, so a malformed file is named once per run rather than
// once per read. Several resolvers ask the same repository different
// questions (its services, its service paths, whether it has any), and
// three copies of one warning reads as three problems.
var warnedDescriptors sync.Map

// loadRepoConfig builds the effective configuration for one
// repository. A descriptor that cannot be parsed is reported once and
// then treated as absent: a malformed file in one repository must not
// abort a fan-out over the others, and silently ignoring it would leave
// the user wondering why their committed policy did nothing.
func loadRepoConfig(r Repository) repoConfig {
	v, err := config.LoadRepoConfig(r.Path)
	if err != nil {
		if _, seen := warnedDescriptors.LoadOrStore(r.Path, true); !seen {
			ui.Warning("%s: %v", r.Name, err)
		}
		return repoConfig{name: r.Name}
	}
	return repoConfig{name: r.Name, repo: v}
}

// String returns a repo-scoped string key: the descriptor's value when
// it sets one, else the central value.
//
// A key the descriptor sets to an empty string still wins, because
// "this repository has no value for that" is a thing a descriptor needs
// to be able to say — an empty `base` opts back in to the repository's
// own default branch even when the project pins one.
func (rc repoConfig) String(key string) string {
	if rc.repo != nil && rc.repo.IsSet(key) {
		return rc.repo.GetString(key)
	}
	return viper.GetString(key)
}

// StringSlice returns a repo-scoped list key. The descriptor's list
// REPLACES the central one rather than appending to it — the layers
// resolve most-specific-wins for a list exactly as they do for a
// scalar, which is also how viper's MergeConfigMap already resolves
// global against project.
//
// Replacement is the semantics the feature exists for. Appending would
// mean a workspace-wide `reviewers: [alice]` kept requesting alice on
// every repository no matter what any repository said, which is the
// behaviour the per-repo layer is here to end. A repository that wants
// the central list plus one more restates it; a repository that wants
// none of it writes `reviewers: []`.
func (rc repoConfig) StringSlice(key string) []string {
	if rc.repo != nil && rc.repo.IsSet(key) {
		return rc.repo.GetStringSlice(key)
	}
	return viper.GetStringSlice(key)
}

// repoKeyed returns a repo-scoped value whose CENTRAL form is a map
// keyed by repository name — `services.<repo>`,
// `cicd.workflows.release.target.<repo>` — and whose descriptor form
// drops that level, because the file already names the repository.
//
// Dropping the level is not cosmetic. Keeping it would let a repository
// configure a different repository by naming it, which is precisely the
// authority a committed file must not have.
//
// Returns nil when neither layer has anything, which every caller
// already treats as "not configured".
func (rc repoConfig) repoKeyed(key string) any {
	if rc.repo != nil && rc.repo.IsSet(key) {
		return rc.repo.Get(key)
	}
	return viper.Get(key + "." + rc.name)
}
