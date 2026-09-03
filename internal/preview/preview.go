// Package preview models preview deployment environments — short-lived,
// per-issue deployments used for review and validation before a change
// reaches the canonical release path. Adapters live in subpackages and
// translate the domain operations onto concrete primitives (workflow
// dispatches, deployment APIs, etc.).
package preview

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"text/template"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/provider"
)

// ConfigGroup is the config key prefix for preview settings
// ("preview.provider", "preview.url_template", "preview.up.workflow", …).
const ConfigGroup = "preview"

// Status is the lifecycle state of a preview environment.
//
// The set is the union of what bosun's adapters can report, which means
// most adapters produce a subset: a reachability probe can only ever say
// Active or Gone, while an adapter reading a deployment API distinguishes
// a provision still in flight from one that was torn down. Callers should
// branch on Alive for the binary question and switch on the specific
// value only where the nuance changes what the user sees.
type Status string

const (
	// StatusUnknown is the zero value: no state was determined. Pairs
	// with Environment.Probed == false.
	StatusUnknown Status = ""

	// StatusCreating means provisioning is in flight. The env is not
	// reachable yet, and that is expected rather than a fault.
	StatusCreating Status = "creating"

	// StatusActive means every service came up.
	StatusActive Status = "active"

	// StatusDegraded means the env is up but some services failed;
	// Environment.FailedServices names them when the adapter knows.
	StatusDegraded Status = "degraded"

	// StatusDeleting means teardown is in flight.
	StatusDeleting Status = "deleting"

	// StatusGone means the env does not exist — torn down, cleaned up,
	// or never provisioned.
	StatusGone Status = "gone"
)

// Alive reports whether the environment is serving traffic. Degraded
// counts: the env is reachable and usable, just incomplete.
func (s Status) Alive() bool {
	return s == StatusActive || s == StatusDegraded
}

// Pending reports whether the environment is mid-transition. A pending
// env is not reachable, but unlike a gone one it is on its way
// somewhere — callers rendering "unreachable" should say so differently.
func (s Status) Pending() bool {
	return s == StatusCreating || s == StatusDeleting
}

// Environment represents a preview deployment bound to a tracker issue.
type Environment struct {
	Name     string // Ephemeral env name, e.g., "brave-falcon".
	URL      string // Resolved URL; empty when no URL template is configured.
	Status   Status // Lifecycle state. Meaningful only when Probed is true.
	Probed   bool   // True when a probe ran and produced a definitive answer.
	IssueKey string // Tracker issue the env is bound to. Empty for Inspect results.

	// FailedServices names the services whose deploy failed, when the
	// adapter can tell. Only populated alongside StatusDegraded.
	FailedServices []string

	// DeployedBy is the account that provisioned the env, when the
	// adapter knows. Empty otherwise — a reachability probe cannot tell.
	DeployedBy string
}

// Alive reports whether the environment was probed and found serving.
// The two-field check (Probed then Status) is the contract every caller
// needs, so it lives here rather than at each call site.
func (e Environment) Alive() bool { return e.Probed && e.Status.Alive() }

// Claim describes a request to create or adopt a preview environment.
type Claim struct {
	IssueKey string // Tracker issue to bind the env to.
	Name     string // Env name; must be non-empty (caller generates a default).

	// There is deliberately no Services field. A "deploy only these"
	// filter leaves the environment half-built: absence from Overrides
	// means "run the default branch", so every service always comes up
	// and a subset is not a coherent request. Per-service information
	// travels in Overrides, which pins tags without narrowing the
	// deploy.

	// Overrides pins per-service image tags (e.g. {"api": "pr-123"}).
	// A service absent from the map runs DefaultBranch.
	Overrides map[string]string

	// DefaultBranch is the branch every unpinned service runs. Empty
	// means "the provider's own default". Adapters whose pipeline has no
	// such concept ignore it.
	DefaultBranch string
}

// Operation names a provider operation whose readiness can be settled
// before it is planned. It exists because readiness is per-operation:
// an adapter can be wired for one half of the lifecycle and not the
// other, so "is this provider configured?" has no single answer.
type Operation string

const (
	// OpCreate is the provisioning half — Provider.Create.
	OpCreate Operation = "create"

	// OpDestroy is the teardown half — Provider.Destroy.
	OpDestroy Operation = "destroy"
)

// Provider defines preview environment operations needed by bosun.
type Provider interface {
	// Ready reports whether the provider can carry out op, without
	// making the attempt. A nil error means op is wired. An error
	// matching ErrNotConfigured means the provider has no backend for
	// it, and callers should skip that step and say why — the same
	// treatment they give the sentinel coming back out of Create or
	// Destroy, just early enough to keep the step out of a plan it
	// could never apply. Any other error is a genuine fault in
	// answering the question and should be surfaced as one.
	//
	// Ready may perform I/O — the workflow-dispatch adapter resolves
	// its targets to answer — but it never changes anything, so a
	// caller may call it before deciding whether to do the expensive
	// input resolution a real call would need.
	Ready(ctx context.Context, op Operation) error

	// Get returns the environment currently bound to issueKey, including
	// a freshness probe when the adapter is able to perform one. Returns
	// ErrNoEnvironment when no env is bound — callers should treat this
	// as the empty-state signal, not an error condition.
	//
	// On an indeterminate probe (timeout, network failure, 5xx),
	// implementations return a ProbeError alongside a partially-populated
	// Environment with Name, URL, and IssueKey set so callers that only
	// need to render the binding can do so. Probed stays false and Status
	// stays StatusUnknown in this case. Callers that strictly need
	// verification can branch on the error.
	//
	// Implementations may quietly drop registry entries that probe as
	// definitively gone (e.g., HTTP 404) to keep the registry honest;
	// such cleanups should be surfaced as informational events rather
	// than reported as errors.
	Get(ctx context.Context, issueKey string) (Environment, error)

	// Inspect probes an env by name without consulting the registry.
	// Used to check whether a user-supplied name is already in use.
	// Returns Environment with IssueKey == "". An empty name returns
	// ErrNoEnvironment.
	Inspect(ctx context.Context, name string) (Environment, error)

	// Create provisions a new environment for the claim. Always
	// triggers the underlying deploy; callers that want to claim an
	// existing env without redeploying should use Adopt instead.
	Create(ctx context.Context, claim Claim) (Environment, error)

	// Adopt records an existing env as bound to issueKey without
	// triggering a deploy. Used when a previously-untracked env is
	// being claimed by an issue (e.g., a user adopting another team
	// member's running preview).
	Adopt(ctx context.Context, issueKey, name string) error

	// Destroy tears down the environment named name, scoped to issueKey
	// (which the adapter uses to clear its registry entry). Idempotent;
	// destroying an env that no longer exists is success.
	Destroy(ctx context.Context, issueKey, name string) error
}

// Lister is the optional discovery half of the provider contract,
// implemented by adapters backed by something that can enumerate
// environments. It is deliberately not part of Provider: an adapter
// whose only view of an env is an HTTP probe against a known URL has no
// way to answer "what exists?", and forcing it to return a stub would
// make the empty result indistinguishable from a genuinely empty fleet.
//
// Callers type-assert and degrade gracefully when the assertion fails.
type Lister interface {
	// List returns every environment the provider can see, including
	// those bound to other issues and other people — the fleet is
	// shared. An empty slice with a nil error means "none exist".
	//
	// IssueKey is empty on every result. The binding registry is keyed
	// by issue, so a name cannot be walked back to one without reading
	// every issue; callers that need the pairing correlate against their
	// own Get.
	List(ctx context.Context) ([]Environment, error)
}

// NameValidator is the optional name-grammar half of the provider
// contract. Preview env names are validated by the caller, never
// silently rewritten by the adapter (a renamed env is one the user
// cannot find again), but the grammar itself is provider knowledge: a
// backend that requires two hyphenated segments rejects names
// ValidateName accepts.
//
// Callers validate through ProviderValidateName, which consults the
// provider when it implements this and falls back to ValidateName.
type NameValidator interface {
	// ValidateName reports whether name is acceptable to this provider,
	// returning an error whose message states the grammar.
	ValidateName(name string) error
}

// ErrNoEnvironment is returned by Get and Inspect when no environment
// is bound or the supplied name is empty. Callers should branch on
// errors.Is rather than treating it as a fatal error.
var ErrNoEnvironment = errors.New("preview: no environment bound to issue")

// ErrAuth reports that the provider rejected bosun's credentials — an
// expired or missing token rather than a transport fault. Callers
// surface it as "re-authenticate" (e.g. `gh auth login`) rather than
// retrying, which is why it is distinct from ProbeError.
var ErrAuth = errors.New("preview: authentication failed")

// ErrNotConfigured reports that the provider has no backend to talk to
// — an unset base URL, a missing pipeline. Callers treat it the way
// they treat a missing optional dependency: skip the preview step and
// say why, rather than failing the command.
var ErrNotConfigured = errors.New("preview: provider not configured")

// ProbeError indicates that a freshness probe ran but produced an
// indeterminate result — 5xx response, network failure, timeout after
// retries. The URL field carries the URL that was probed so callers can
// report it (e.g., in a --force "couldn't verify X, proceeding" notice)
// or decide whether to proceed without verification.
type ProbeError struct {
	URL string
	Err error
}

func (e *ProbeError) Error() string {
	return fmt.Sprintf("probing %s: %v", e.URL, e.Err)
}

func (e *ProbeError) Unwrap() error { return e.Err }

// nameRe approximates Kubernetes subdomain rules: lowercase letter start,
// lowercase alphanumerics or hyphens, alphanumeric end, max 63 chars.
var nameRe = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateName reports whether name is acceptable as a preview env name.
// Rules approximate Kubernetes subdomain labels: 1–63 chars, lowercase
// letters/digits/hyphens, starting with a letter and ending alphanumeric.
//
// This is the floor every provider agrees on. A provider with a stricter
// grammar declares it by implementing NameValidator; callers should go
// through ProviderValidateName rather than calling this directly, so the
// stricter rule is the one the user is held to.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: must be lowercase letters, digits, and hyphens; start with a letter, end alphanumeric, max 63 chars", name)
	}
	return nil
}

// ProviderValidateName validates name against p's grammar when p
// declares one, and against ValidateName otherwise. A nil provider
// falls back to ValidateName too, so display paths that never built one
// still validate.
func ProviderValidateName(p Provider, name string) error {
	if v, ok := p.(NameValidator); ok {
		return v.ValidateName(name)
	}
	return ValidateName(name)
}

// --- Templates ---

// Ref is how a preview environment appears inside a user-facing
// template. It is the `preview` half of bosun's shared template
// vocabulary: {{.Preview.Name}} means the same thing in a URL
// template, a notification template, and anywhere else a template
// context carries one.
//
// It lives here rather than in the CLI because the adapters render
// URLTemplate themselves and cannot import the CLI. Three copies of
// the same shape in three packages is precisely how the vocabulary
// fragmented before — {{.Name}} in a URL template against
// {{.PreviewName}} in a notification — so there is one type and the
// packages that render share it.
type Ref struct {
	Name string // Ephemeral environment name, e.g. "brave-falcon".
	URL  string // Rendered environment URL.
}

// URLTemplateData is the context passed to <stage>.url_template.
//
// Ref.URL is empty here: this is the template that computes it. The
// field stays visible rather than being split into a narrower type,
// because a second shape is how the split starts.
type URLTemplateData struct {
	Preview Ref
}

// --- Provider registration ---

// Target identifies a workflow the cicd adapter should dispatch to.
// It lives here rather than in that adapter because Deps carries it
// across the CLI-to-adapter seam: resolving a target means reading
// workflow config and intersecting it with the active workspace's
// repositories, which is the CLI's knowledge, not an adapter's.
type Target struct {
	Owner    string
	Repo     string
	Workflow string
	Label    string // Display label (typically the local repo name).
}

// WorkflowDeps is the workflow-dispatch wiring, supplied for adapters
// that provision by dispatching a pipeline workflow. Adapters that talk
// to a deployment API instead leave it untouched.
type WorkflowDeps struct {
	// Pipeline dispatches the workflows. Nil means no pipeline is
	// configured; adapters that need one return ErrNotConfigured.
	Pipeline cicd.CICD

	// Stage is the lifecycle stage prefix, typically "preview".
	Stage string

	// Targets resolves the workflows to dispatch for a sub-stage
	// ("preview.up", "preview.down").
	Targets func(ctx context.Context, subStage string) ([]Target, error)

	// InputName maps a sub-stage and a concept ("name", "issue",
	// "services") to the workflow input key configured for it, or "" when
	// none is.
	InputName func(subStage, concept string) string
}

// Deps carries the runtime wiring a preview adapter needs and cannot
// read out of config on its own: the tracker that stores env-to-issue
// bindings, the URL template, and the workflow plumbing above. The CLI
// resolves these from the active workspace and hands them to the
// services registry, which passes them to whichever provider is
// selected. Adapters read the fields they need and ignore the rest.
type Deps struct {
	// Tracker is the binding registry. Nil means bindings can't be
	// persisted — adapters degrade to stateless operation rather than
	// failing.
	Tracker issue.Tracker

	// URLTemplate renders an env's URL from its name. Nil means the
	// adapter has no local way to build a URL.
	URLTemplate *template.Template

	// Workflow is the dispatch plumbing; see WorkflowDeps.
	Workflow WorkflowDeps
}

// ProviderDescriptor is what a preview provider package contributes to
// bosun: the config value that selects it, the configuration it needs,
// and how to build it. The services registry is the only thing that
// collects descriptors; nothing else knows which providers exist.
type ProviderDescriptor struct {
	// Name is the value that selects this provider in preview.provider,
	// e.g. "cicd".
	Name string

	// Keys are the provider-specific config keys under the "preview"
	// group, relative to it (e.g. "api.base_url"). They are spliced into
	// bosun's config schema so init, doctor, and `config check` cover
	// them without knowing which provider is configured.
	Keys []provider.ConfigKey

	// New constructs the provider from configuration and the CLI-resolved
	// dependencies.
	New func(provider.Config, Deps) (Provider, error)
}
