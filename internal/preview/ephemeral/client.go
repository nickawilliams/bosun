package ephemeral

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nickawilliams/bosun/internal/preview"
)

// deployment is one entry of GET /api/deployments. It mirrors the
// server's DeploymentListEntry; fields bosun has no use for (avatars,
// the synthetic row id, PR enrichment) are left out, and the ones that
// can legitimately be absent are pointers or slices so a missing key is
// distinguishable from an empty one.
type deployment struct {
	Name           *string  `json:"name"`
	URL            *string  `json:"url"`
	Status         string   `json:"status"`
	DeployedBy     string   `json:"deployedBy"`
	CreatedAt      string   `json:"createdAt"`
	RunURL         string   `json:"runUrl"`
	FailedServices []string `json:"failedServices"`
}

// deploymentsResponse is the GET /api/deployments envelope.
type deploymentsResponse struct {
	Deployments []deployment `json:"deployments"`
}

// deployRequest is the POST /api/deploy body.
//
// ImageOverrides is a string, not a map, and that is the API's shape
// rather than an oversight on this side: the server forwards the value
// straight into a workflow_dispatch input, and GitHub only accepts
// strings there. Sending an object gets it stringified into something
// the workflow cannot parse.
type deployRequest struct {
	EphemeralName  string `json:"ephemeralName,omitempty"`
	DefaultBranch  string `json:"defaultBranch,omitempty"`
	ImageOverrides string `json:"imageOverrides,omitempty"`
}

// deleteRequest is the POST /api/delete-deployment body.
type deleteRequest struct {
	EphemeralName string `json:"ephemeralName"`
}

// apiError is the error envelope every handler returns. Both fields are
// optional in practice, so message() falls back to the status line.
type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details"`
}

func (e apiError) message() string {
	for _, s := range []string{e.Details, e.Message, e.Error} {
		if s != "" {
			return s
		}
	}
	return ""
}

// statusError reports a response the API answered but bosun cannot use.
// It carries the code so callers can classify without re-reading the
// body.
type statusError struct {
	Code int
	Body string
	URL  string
}

func (e *statusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s: HTTP %d", e.URL, e.Code)
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.URL, e.Code, e.Body)
}

// retryable reports whether the failure is one a second attempt might
// clear: the server is there but not answering usefully. 4xx responses
// are the server's considered answer and are not retried.
func (e *statusError) retryable() bool { return e.Code >= 500 }

// do issues a request against path and decodes a JSON response into out
// (which may be nil to discard the body).
//
// Every failure comes back as one of three shapes so callers can map
// them onto the provider contract without inspecting HTTP details:
// preview.ErrAuth for 401/403, *statusError for any other non-2xx, and
// a transport error otherwise.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	endpoint, err := c.resolve(path)
	if err != nil {
		return err
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return err
	}
	token, err := c.token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	// Read before branching: the error envelope and the success payload
	// come off the same body, and an unread body strands the connection
	// instead of returning it to the pool.
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w: %s", preview.ErrAuth, describe(raw, resp.Status))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{Code: resp.StatusCode, Body: describe(raw, resp.Status), URL: endpoint}
	}
	if readErr != nil {
		return fmt.Errorf("reading response: %w", readErr)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decoding %s response: %w", errUndecodable, path, err)
	}
	return nil
}

// errUndecodable marks a 2xx whose body isn't the shape this adapter
// expects. Definitive, not transient: the two ways to get one are a
// base URL pointing at something else (the service's own SPA fallback
// answers 200 with HTML) and a server-side schema change. Retrying
// either costs a request and reports the wrong diagnosis.
var errUndecodable = errors.New("unexpected response body")

// resolve joins path onto the configured base URL.
//
// Both failure arms report preview.ErrNotConfigured rather than letting
// the request go out and fail: a base URL with no scheme, or one that
// isn't a URL at all, is a config mistake, and surfacing it as a
// retried "couldn't verify" sends the user looking at the network.
//
// The base is concatenated, not reparsed and re-serialized: round-tripping
// through url.URL would quietly relocate a query or fragment to the wrong
// side of the path.
func (c *client) resolve(path string) (string, error) {
	if c.baseURL == "" {
		return "", fmt.Errorf("%w: set %s.base_url", preview.ErrNotConfigured, preview.ConfigGroup)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid %s.base_url %q: %w",
			preview.ErrNotConfigured, preview.ConfigGroup, c.baseURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("%w: %s.base_url %q needs a scheme and host (e.g. https://host)",
			preview.ErrNotConfigured, preview.ConfigGroup, c.baseURL)
	}
	return c.baseURL + path, nil
}

// token resolves the bearer token, caching it for the client's lifetime.
// A command issues several requests against the API and shelling out to
// the GitHub CLI for each one is both slow and pointless — the token
// does not change mid-command.
func (c *client) token(ctx context.Context) (string, error) {
	c.tokenOnce.Do(func() {
		c.cachedToken, c.tokenErr = c.tokenSource(ctx)
		if c.tokenErr == nil && strings.TrimSpace(c.cachedToken) == "" {
			c.tokenErr = fmt.Errorf("%w: no GitHub token available (run `gh auth login`)", preview.ErrAuth)
		}
	})
	return c.cachedToken, c.tokenErr
}

// describe renders a response body as a one-line reason, preferring the
// API's own error text and falling back to the HTTP status line.
func describe(raw []byte, status string) string {
	var env apiError
	if err := json.Unmarshal(raw, &env); err == nil {
		if msg := env.message(); msg != "" {
			return msg
		}
	}
	if trimmed := strings.TrimSpace(string(raw)); trimmed != "" && len(trimmed) <= 200 {
		return trimmed
	}
	return status
}

// probeError wraps err as the indeterminate-probe result the provider
// contract expects, tagged with the endpoint that failed so callers can
// name it in a --force notice.
//
// The endpoint is built rather than re-resolved: every caller reaches
// here having already made a request, so the base URL is known good —
// resolve's failures are config errors, and those are reported as
// ErrNotConfigured long before anything is called indeterminate.
func (c *client) probeError(path string, err error) error {
	return &preview.ProbeError{URL: c.baseURL + path, Err: err}
}

// indeterminate reports whether err means "couldn't get an answer"
// rather than "the answer is no". Transport failures and 5xx qualify;
// an authentication rejection does not — it is definitive, and callers
// must see preview.ErrAuth rather than a retry suggestion.
func indeterminate(err error) bool {
	if err == nil ||
		errors.Is(err, preview.ErrAuth) ||
		errors.Is(err, preview.ErrNotConfigured) ||
		errors.Is(err, errUndecodable) {
		return false
	}
	var se *statusError
	if errors.As(err, &se) {
		return se.retryable()
	}
	return true
}
