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
)

// Environment represents a preview deployment bound to a tracker issue.
type Environment struct {
	Name     string // Ephemeral env name, e.g., "brave-falcon".
	URL      string // Resolved URL; empty when no URL template is configured.
	Alive    bool   // Probed result. Meaningful only when Probed is true.
	Probed   bool   // True when a probe ran and produced a definitive answer.
	IssueKey string // Tracker issue the env is bound to. Empty for Inspect results.
}

// Claim describes a request to create or adopt a preview environment.
type Claim struct {
	IssueKey  string            // Tracker issue to bind the env to.
	Name      string            // Env name; must be non-empty (caller generates a default).
	Services  []string          // Services affected by the deploy.
	Overrides map[string]string // service → image tag overrides (e.g., {"api": "pr-123"}).
}

// Provider defines preview environment operations needed by bosun.
type Provider interface {
	// Get returns the environment currently bound to issueKey, including
	// a freshness probe when the adapter is able to perform one. Returns
	// ErrNoEnvironment when no env is bound — callers should treat this
	// as the empty-state signal, not an error condition.
	//
	// On an indeterminate probe (timeout, network failure, 5xx),
	// implementations return a ProbeError alongside a partially-populated
	// Environment with Name, URL, and IssueKey set so callers that only
	// need to render the binding can do so. Probed and Alive remain false
	// in this case. Callers that strictly need verification can branch on
	// the error.
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

// ErrNoEnvironment is returned by Get and Inspect when no environment
// is bound or the supplied name is empty. Callers should branch on
// errors.Is rather than treating it as a fatal error.
var ErrNoEnvironment = errors.New("preview: no environment bound to issue")

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
func ValidateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: must be lowercase letters, digits, and hyphens; start with a letter, end alphanumeric, max 63 chars", name)
	}
	return nil
}
