// Package ephemeral implements preview.Provider against the HTTP API
// that fronts Clearstory's ephemeral-environment service.
//
// It exists because the same environments are managed by a web UI whose
// backend already does the hard part — recovering the generated env name
// from workflow logs, reading Kubernetes annotations for the durable
// view, parsing the deploy matrix for per-service failures — and none of
// that is worth reimplementing in Go. Where the sibling cicd adapter
// dispatches workflows and then probes a URL to guess whether the env is
// up, this adapter asks.
//
// The observable difference is the status taxonomy: a probe can only
// report active or gone, while the API distinguishes a provision still
// in flight (creating), a partial deploy (degraded, with the failed
// services named), and a teardown in progress (deleting). Everything
// else about the contract — registry as source of truth, Create
// returning before the deploy lands, Destroy refusing an empty name —
// is deliberately identical, so switching providers changes what bosun
// can see, not how it behaves.
package ephemeral

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/nickawilliams/bosun/internal/preview"
)

// Endpoint paths, named so the error strings and the tests agree with
// the routes.
const (
	pathDeploy      = "/api/deploy"
	pathDelete      = "/api/delete-deployment"
	pathDeployments = "/api/deployments"
)

// maxResponseBytes caps a decoded response. The deployments listing
// grows with the fleet and carries per-service override detail, so the
// ceiling is generous; it exists to bound a misconfigured base URL
// pointed at something that streams, not to police normal payloads.
const maxResponseBytes = 8 << 20

// defaultTimeout bounds a single API call.
//
// It is this large because POST /api/deploy is synchronous past the
// workflow dispatch: the server polls GitHub for up to ten seconds
// looking for the run it just created before it answers. A timeout
// under that would abandon a deploy that had already been triggered,
// leaving an env running with no binding recorded for it.
const defaultTimeout = 30 * time.Second

// Options configures the adapter. BaseURL is required; everything else
// has a working default or is optional.
type Options struct {
	// BaseURL is the API root, e.g. https://ephemeral-ui.example.dev.
	BaseURL string

	// Token resolves the bearer token. Nil uses the GitHub CLI (see
	// GitHubCLIToken).
	Token func(ctx context.Context) (string, error)

	// HTTPClient issues the requests. Nil uses a client with
	// defaultTimeout.
	HTTPClient *http.Client

	// Tracker stores the env-to-issue binding. Nil means bindings aren't
	// persisted; Get then reports no environment.
	Tracker preview.PropertyStore

	// URLTemplate renders an env's URL from its name. When nil the
	// adapter falls back to the URL the API reports, which is only
	// available for environments that already exist.
	URLTemplate *template.Template
}

// New returns a Provider backed by the API at opts.BaseURL.
func New(opts Options) preview.Provider {
	tokenSource := opts.Token
	if tokenSource == nil {
		tokenSource = GitHubCLIToken
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &adapter{
		client: client{
			baseURL:     strings.TrimRight(opts.BaseURL, "/"),
			httpClient:  httpClient,
			tokenSource: tokenSource,
		},
		binding:     preview.Binding{Store: opts.Tracker},
		urlTemplate: opts.URLTemplate,
	}
}

// client holds the transport half: base URL, HTTP client, and the
// once-resolved bearer token.
type client struct {
	baseURL     string
	httpClient  *http.Client
	tokenSource func(ctx context.Context) (string, error)

	tokenOnce   sync.Once
	cachedToken string
	tokenErr    error
}

type adapter struct {
	client
	binding     preview.Binding
	urlTemplate *template.Template
}

// urlData is the template context passed to URLTemplate. It matches the
// cicd adapter's so one configured template serves both providers.
type urlData struct {
	Name string
}

func (p *adapter) Get(ctx context.Context, issueKey string) (preview.Environment, error) {
	name := p.binding.Name(ctx, issueKey)
	if name == "" {
		return preview.Environment{}, preview.ErrNoEnvironment
	}

	// The binding is the source of truth and the lookup is advisory, so
	// every failure arm returns the binding alongside the error: a
	// caller that only needs to render what's bound can, and one that
	// needs a verified state branches on the error.
	env := preview.Environment{Name: name, IssueKey: issueKey, URL: p.renderURL(name)}

	found, err := p.lookup(ctx, name)
	if err != nil {
		return env, err
	}
	if found == nil {
		// Definitively absent. Left bound on purpose: teardown may have
		// happened, or a deploy triggered moments ago may not be listed
		// yet, and a read must not clobber either. Recreating under the
		// stored name is the preview command's job.
		env.Probed = true
		env.Status = preview.StatusGone
		return env, nil
	}
	return p.merge(env, *found), nil
}

func (p *adapter) Inspect(ctx context.Context, name string) (preview.Environment, error) {
	if name == "" {
		return preview.Environment{}, preview.ErrNoEnvironment
	}
	env := preview.Environment{Name: name, URL: p.renderURL(name)}

	found, err := p.lookup(ctx, name)
	if err != nil {
		return env, err
	}
	if found == nil {
		// A name nobody is using is the answer Inspect exists to give,
		// not an error — ErrNoEnvironment is reserved for an empty
		// argument. Callers read Probed plus Status to tell "free" from
		// "couldn't check".
		env.Probed = true
		env.Status = preview.StatusGone
		return env, nil
	}
	return p.merge(env, *found), nil
}

func (p *adapter) Create(ctx context.Context, claim preview.Claim) (preview.Environment, error) {
	// The API will generate a name when given none, but it only reports
	// it back through a separate workflow-status call once the setup job
	// has logged it. Requiring the caller's name keeps the binding
	// writable here and keeps Name meaning stable identity.
	if claim.Name == "" {
		return preview.Environment{}, errors.New("preview: claim name is empty")
	}

	body := deployRequest{
		EphemeralName: claim.Name,
		DefaultBranch: claim.DefaultBranch,
	}
	overrides, err := encodeOverrides(claim.Overrides)
	if err != nil {
		return preview.Environment{}, err
	}
	body.ImageOverrides = overrides

	if err := p.do(ctx, http.MethodPost, pathDeploy, body, nil); err != nil {
		return preview.Environment{}, fmt.Errorf("triggering deploy: %w", err)
	}

	// Binding write is best-effort, matching the cicd adapter: the
	// deploy is already in flight and failing here would report a
	// successful provision as an error.
	_ = p.binding.Bind(ctx, claim.IssueKey, claim.Name)

	// Deliberately unprobed. The response means "dispatched", not
	// "serving", and callers render the URL before the env is up.
	return preview.Environment{
		Name:     claim.Name,
		URL:      p.renderURL(claim.Name),
		IssueKey: claim.IssueKey,
	}, nil
}

func (p *adapter) Adopt(ctx context.Context, issueKey, name string) error {
	if name == "" {
		return errors.New("preview: adopt name is empty")
	}
	return p.binding.Bind(ctx, issueKey, name)
}

func (p *adapter) Destroy(ctx context.Context, issueKey, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("preview: refusing to destroy without an env name")
	}

	err := p.do(ctx, http.MethodPost, pathDelete, deleteRequest{EphemeralName: name}, nil)
	if err != nil && !alreadyGone(err) {
		return fmt.Errorf("triggering teardown: %w", err)
	}

	// Best-effort, matching the cicd adapter: the teardown is dispatched
	// and reporting a binding-cleanup failure as a teardown failure
	// would invite a retry that tears down nothing.
	_ = p.binding.Unbind(ctx, issueKey)
	return nil
}

// List returns every environment the API can see — the fleet is shared,
// so this includes other people's. IssueKey is left empty: the binding
// registry is keyed by issue, not by name, so an env's owning issue
// can't be recovered without walking every issue.
func (p *adapter) List(ctx context.Context) ([]preview.Environment, error) {
	entries, err := p.deployments(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]preview.Environment, 0, len(entries))
	for _, d := range entries {
		if d.Name == nil || *d.Name == "" {
			// A provision run whose name hasn't been recovered from the
			// job logs yet. There is nothing to address it by, so it
			// can't be adopted, torn down, or matched to an issue.
			continue
		}
		env := preview.Environment{Name: *d.Name, URL: p.renderURL(*d.Name)}
		out = append(out, p.merge(env, d))
	}
	return out, nil
}

// nameRe is the API's own grammar: two or more hyphen-separated
// lowercase alphanumeric segments, mirroring the namespace validation in
// the provisioning workflow. A single word is rejected, which is the one
// place this provider is stricter than preview.ValidateName.
var nameRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)+$`)

// ValidateName enforces the API's grammar on top of the shared floor, so
// a name that would come back as a 400 is refused before a deploy is
// dispatched.
func (p *adapter) ValidateName(name string) error {
	if err := preview.ValidateName(name); err != nil {
		return err
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: must be two or more hyphenated lowercase parts (e.g. brave-falcon)", name)
	}
	return nil
}

// lookup finds the deployment named name, or nil when the API lists no
// such env. Errors are already mapped onto the provider contract.
func (p *adapter) lookup(ctx context.Context, name string) (*deployment, error) {
	entries, err := p.deployments(ctx)
	if err != nil {
		return nil, err
	}
	// Names are case-insensitive to the backend — it lowercases them for
	// its pending-deletion bookkeeping — so match the same way rather
	// than reporting a differently-cased env as absent.
	for _, d := range entries {
		if d.Name != nil && strings.EqualFold(*d.Name, name) {
			return &d, nil
		}
	}
	return nil, nil
}

// deployments fetches the listing, retrying once on an indeterminate
// failure and translating what survives into the provider contract:
// preview.ErrAuth passes through for the CLI to turn into a re-auth
// prompt, anything else indeterminate becomes a *preview.ProbeError
// naming the endpoint.
func (p *adapter) deployments(ctx context.Context) ([]deployment, error) {
	var lastErr error
	for range 2 {
		var resp deploymentsResponse
		err := p.do(ctx, http.MethodGet, pathDeployments, nil, &resp)
		if err == nil {
			return resp.Deployments, nil
		}
		lastErr = err
		if !indeterminate(err) {
			return nil, err
		}
		if ctx.Err() != nil {
			// The caller's deadline is gone; a second attempt would fail
			// identically and only delay the report.
			break
		}
	}
	return nil, p.probeError(pathDeployments, lastErr)
}

// merge folds an API entry into the environment shell built from the
// binding. Only the fields the API is authoritative for are taken:
// Name stays as the caller knows it, and the templated URL wins over
// the API's when one is configured, so a URL renders identically
// whether the env exists yet or not.
func (p *adapter) merge(env preview.Environment, d deployment) preview.Environment {
	status, known := translateStatus(d.Status)
	env.Status = status
	// An unrecognized status means a taxonomy this build predates.
	// Reporting it as unprobed sends callers down the "exists but
	// couldn't be verified" path, which redeploys under the stored name
	// — the safe direction. Claiming Probed would render a live env as
	// torn down.
	env.Probed = known
	env.DeployedBy = d.DeployedBy
	if status == preview.StatusDegraded {
		env.FailedServices = d.FailedServices
	}
	if env.URL == "" && d.URL != nil {
		env.URL = *d.URL
	}
	return env
}

// renderURL builds the env's URL from the configured template, or ""
// when there is no template. Kept local so the CLI can show a URL
// before the deploy has produced one.
func (p *adapter) renderURL(name string) string {
	if p.urlTemplate == nil || name == "" {
		return ""
	}
	var buf strings.Builder
	if err := p.urlTemplate.Execute(&buf, urlData{Name: name}); err != nil {
		return ""
	}
	return buf.String()
}

// translateStatus maps an API status onto the domain enum, reporting
// whether the value was recognized.
func translateStatus(s string) (preview.Status, bool) {
	switch s {
	case "creating":
		return preview.StatusCreating, true
	case "active":
		return preview.StatusActive, true
	case "degraded":
		return preview.StatusDegraded, true
	case "deleting":
		return preview.StatusDeleting, true
	case "cleaned_up":
		return preview.StatusGone, true
	default:
		return preview.StatusUnknown, false
	}
}

// encodeOverrides renders the per-service tag pins as the JSON string
// the API forwards to the workflow. An empty map yields "", which the
// API reads as "every service runs the default branch".
func encodeOverrides(overrides map[string]string) (string, error) {
	if len(overrides) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(overrides)
	if err != nil {
		return "", fmt.Errorf("encoding image overrides: %w", err)
	}
	return string(encoded), nil
}

// alreadyGone reports whether a teardown failure means the env was not
// there to begin with, which Destroy treats as success — it is
// documented idempotent.
func alreadyGone(err error) bool {
	var se *statusError
	return errors.As(err, &se) && se.Code == http.StatusNotFound
}

// Verify the provider satisfies the interfaces it claims at compile time.
var (
	_ preview.Provider      = (*adapter)(nil)
	_ preview.Lister        = (*adapter)(nil)
	_ preview.NameValidator = (*adapter)(nil)
)
