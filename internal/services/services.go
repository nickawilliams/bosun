// Package services owns provider construction. It is the one place in
// bosun that knows which providers exist — the registries below are the
// complete list — and the only package that imports a provider adapter.
//
// Everything above it (the CLI, in particular) asks for a capability and
// gets an interface back: services.IssueTracker(cfg) rather than
// jira.New(...).
//
// # Adding a provider
//
// Adding an issue tracker (the other capabilities follow the same shape):
//
//  1. Write internal/issue/<provider>/ implementing issue.Tracker.
//  2. Export a Descriptor() returning an issue.TrackerDescriptor — the
//     config value that selects it, the config keys it needs, its issue-key
//     grammar, and a constructor taking provider.Config.
//  3. Add one line to trackerDescriptors below.
//
// Nothing in internal/cli changes. The provider's keys are spliced into
// the config schema, its name becomes a selectable option in `bosun init`,
// `bosun doctor` probes it through issue.Tracker.AuthTest, and issue keys
// parse with its own grammar — all read off the registry.
package services

import (
	"fmt"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/cicd/githubactions"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/issue/jira"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/notify/slack"
	"github.com/nickawilliams/bosun/internal/provider"
)

// --- Registries: the complete set of providers bosun ships. ---
//
// Each list below is the complete set for one capability. Adding a
// provider is one entry plus its own package.

var trackerDescriptors = []issue.TrackerDescriptor{
	jira.Descriptor(),
}

var hostDescriptors = []code.HostDescriptor{
	github.Descriptor(),
}

var notifierDescriptors = []notify.NotifierDescriptor{
	slack.Descriptor(),
}

var pipelineDescriptors = []cicd.Descriptor{
	githubactions.Descriptor(),
}

var trackers = newRegistry("issue tracker", issue.ConfigGroup,
	entries(trackerDescriptors, func(d issue.TrackerDescriptor) entry[issue.Tracker] {
		return entry[issue.Tracker]{name: d.Name, keys: d.Keys, new: d.New}
	})...)

var hosts = newRegistry("code host", code.ConfigGroup,
	entries(hostDescriptors, func(d code.HostDescriptor) entry[code.Host] {
		return entry[code.Host]{name: d.Name, keys: d.Keys, new: d.New}
	})...)

var notifiers = newRegistry("notification provider", notify.ConfigGroup,
	entries(notifierDescriptors, func(d notify.NotifierDescriptor) entry[notify.Notifier] {
		return entry[notify.Notifier]{name: d.Name, keys: d.Keys, new: d.New}
	})...)

var pipelines = newRegistry("CI/CD provider", cicd.ConfigGroup,
	entries(pipelineDescriptors, func(d cicd.Descriptor) entry[cicd.CICD] {
		return entry[cicd.CICD]{name: d.Name, keys: d.Keys, new: d.New}
	})...)

// catalogs indexes every registry by its config group so the config
// layer can ask about a group's providers without knowing the
// capability's type. Keep in sync with the registries above.
var catalogs = map[string]catalog{
	issue.ConfigGroup:  trackers,
	code.ConfigGroup:   hosts,
	notify.ConfigGroup: notifiers,
	cicd.ConfigGroup:   pipelines,
}

// --- Construction ---

// IssueTracker builds the configured issue tracker.
//
// The provider pick is resolved first — prompted for when unset and the
// session is interactive — because the rest of the tracker's config
// depends on which provider it is. The adapter then requires whatever
// else it needs.
func IssueTracker(cfg provider.Config) (issue.Tracker, error) {
	if err := cfg.Require(trackers.providerKey); err != nil {
		return nil, err
	}
	return trackers.build(cfg, cfg.Get(trackers.providerKey))
}

// CodeHost builds the configured code host.
//
// An unset provider falls back to the sole registered host instead of
// prompting: hosts discover credentials on their own (a developer with
// `gh auth login` already done is never asked anything), and every
// repository-touching command needs one.
func CodeHost(cfg provider.Config) (code.Host, error) {
	return hosts.build(cfg, hosts.configured(cfg))
}

// CICD builds the configured CI/CD pipeline. Same unset-provider
// fallback as CodeHost, for the same reason — GitHub Actions shares the
// code host's credentials and needs no prompting of its own.
func CICD(cfg provider.Config) (cicd.CICD, error) {
	return pipelines.build(cfg, pipelines.configured(cfg))
}

// Notifier builds the configured notification provider. Unlike the other
// capabilities this one is strictly opt-in: an unset provider is an
// error callers treat as "skip notifications", never a prompt.
func Notifier(cfg provider.Config) (notify.Notifier, error) {
	name := cfg.Get(notifiers.providerKey)
	if name == "" {
		return nil, fmt.Errorf("notification provider not configured")
	}
	return notifiers.build(cfg, name)
}

// ParseIssueIdentifier extracts the configured tracker's issue key from
// s — a branch name, a workspace directory — and returns "" when the
// string carries none, or when no tracker provider can be determined.
//
// Deliberately not routed through a constructed Tracker: key shape is
// pure grammar, and the callers are display paths that run for every
// command. Building a tracker here would ask an unconfigured user for
// credentials to render a breadcrumb. An unset provider falls back to the
// sole registered one for the same reason CodeHost does — with a single
// choice there is nothing to ask about.
func ParseIssueIdentifier(cfg provider.Config, s string) string {
	name := trackers.configured(cfg)
	for _, d := range trackerDescriptors {
		if d.Name == name && d.ParseIdentifier != nil {
			return d.ParseIdentifier(s)
		}
	}
	return ""
}

// --- Provider metadata, for the config layer ---

// ProviderNames returns the providers registered for a config group, in
// registration order. Returns nil for a group that has no providers
// (workspace, display, …). The config schema renders these as the
// selectable values of the group's "provider" key, so a newly
// registered provider shows up in `bosun init` with no schema edit.
func ProviderNames(group string) []string {
	c, ok := catalogs[group]
	if !ok {
		return nil
	}
	return c.names()
}

// ProviderKeys returns the config keys the named provider contributes to
// its group, relative to it (e.g. "base_url"). Returns nil for an
// unknown group or provider.
func ProviderKeys(group, name string) []provider.ConfigKey {
	c, ok := catalogs[group]
	if !ok {
		return nil
	}
	return c.keys(name)
}

// HasProvider reports whether name is a registered provider for a config
// group. Callers validating configuration use it instead of a switch
// over provider names.
func HasProvider(group, name string) bool {
	c, ok := catalogs[group]
	if !ok {
		return false
	}
	return c.has(name)
}

// SoleProvider returns the only provider registered for a group, or ""
// when the group has none or more than one. The config layer uses it to
// decide which provider's keys to show before the user has picked one:
// with a single choice there is nothing to pick, so its keys are shown
// straight away.
func SoleProvider(group string) string {
	c, ok := catalogs[group]
	if !ok {
		return ""
	}
	return c.sole()
}

// --- Registry plumbing ---

// entry is one provider's registration, flattened out of whichever
// capability descriptor it came from.
type entry[T any] struct {
	name string
	keys []provider.ConfigKey
	new  func(provider.Config) (T, error)
}

// entries maps a capability's descriptor list to registry entries. The
// per-capability conversion is a one-liner at each call site because the
// descriptor types deliberately aren't a common shape — a tracker
// describes things a pipeline has no notion of.
func entries[D any, T any](ds []D, conv func(D) entry[T]) []entry[T] {
	out := make([]entry[T], len(ds))
	for i, d := range ds {
		out[i] = conv(d)
	}
	return out
}

// registry indexes one capability's providers by name.
type registry[T any] struct {
	// label names the capability in error messages ("issue tracker").
	label string
	// providerKey is the config key that selects a provider
	// ("issue_tracker.provider").
	providerKey string

	order   []string
	entries map[string]entry[T]
}

func newRegistry[T any](label, group string, entries ...entry[T]) *registry[T] {
	r := &registry[T]{
		label:       label,
		providerKey: group + ".provider",
		entries:     make(map[string]entry[T], len(entries)),
	}
	for _, e := range entries {
		r.order = append(r.order, e.name)
		r.entries[e.name] = e
	}
	return r
}

// build constructs the named provider, or reports it as unsupported.
func (r *registry[T]) build(cfg provider.Config, name string) (T, error) {
	e, ok := r.entries[name]
	if !ok {
		var zero T
		return zero, fmt.Errorf("unsupported %s: %q", r.label, name)
	}
	return e.new(cfg)
}

// configured returns the provider named in config, falling back to the
// sole registered provider when config is silent.
func (r *registry[T]) configured(cfg provider.Config) string {
	if name := cfg.Get(r.providerKey); name != "" {
		return name
	}
	return r.sole()
}

func (r *registry[T]) names() []string {
	return append([]string(nil), r.order...)
}

func (r *registry[T]) keys(name string) []provider.ConfigKey {
	e, ok := r.entries[name]
	if !ok {
		return nil
	}
	return append([]provider.ConfigKey(nil), e.keys...)
}

func (r *registry[T]) has(name string) bool {
	_, ok := r.entries[name]
	return ok
}

func (r *registry[T]) sole() string {
	if len(r.order) != 1 {
		return ""
	}
	return r.order[0]
}

// catalog is the type-erased view of a registry that the config layer
// needs — everything except construction, which is the only part that
// knows the capability's type.
type catalog interface {
	names() []string
	keys(name string) []provider.ConfigKey
	has(name string) bool
	sole() string
}
