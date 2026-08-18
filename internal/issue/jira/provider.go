package jira

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in
// issue_tracker.provider, and the name it goes by in doctor rows.
const providerName = "jira"

// Descriptor registers the Jira adapter with the services registry: the
// config it needs, and how to build it from that config.
func Descriptor() issue.TrackerDescriptor {
	return issue.TrackerDescriptor{
		Name: providerName,
		Keys: []provider.ConfigKey{
			{Key: "base_url", Label: "base URL", Example: "https://mycompany.atlassian.net", Required: true},
			{Key: "email", Label: "email", Required: true},
			{Key: "token", Label: "API token", EnvVar: "BOSUN_JIRA_TOKEN", Secret: true, Required: true},
		},
		ParseIdentifier: ParseIdentifier,
		New: func(cfg provider.Config) (issue.Tracker, error) {
			// Jira has no credential discovery of its own (no CLI to
			// borrow a token from), so every value comes from config —
			// prompted for on a TTY, an error otherwise.
			if err := cfg.Require(issue.ConfigGroup); err != nil {
				return nil, err
			}
			return New(
				cfg.Get(issue.ConfigGroup+".base_url"),
				cfg.Get(issue.ConfigGroup+".email"),
				cfg.Get(issue.ConfigGroup+".token"),
			), nil
		},
	}
}

// identifierPattern matches a Jira issue key: an uppercase project key
// followed by a hyphen and the issue number (PROJ-123, CS-42).
var identifierPattern = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d+`)

// ParseIdentifier finds a Jira issue key inside s — a branch name like
// "feature/PROJ-123_add-widget", a workspace path, a commit subject.
func ParseIdentifier(s string) string {
	return identifierPattern.FindString(s)
}

// probeKey is the issue AuthTest asks for. It only needs to be a
// well-formed key that no real project uses: the point is to reach an
// authenticated endpoint, and a 404 answers the auth question just as
// well as a hit does.
const probeKey = "BOSUN-0"

// AuthTest verifies the configured credentials by fetching probeKey and
// returns the site and account it authenticated as. A definitive
// not-found counts as success — the request was authenticated, the probe
// issue simply doesn't exist — and reports the endpoint identity, which
// is the healthy outcome against a real Jira site.
//
// The not-found arm keys off issue.ErrNotFound rather than the HTTP
// status: GetIssue already translates 404 into that sentinel, so the
// status never reaches the error string here. (Matching on "404" is what
// this check used to do from the CLI side, which meant a healthy site
// reported "connection failed" for the one response it was most likely
// to give.)
func (a *Adapter) AuthTest(ctx context.Context) (string, error) {
	_, err := a.GetIssue(ctx, probeKey)
	switch {
	case err == nil:
		return providerName + " · authenticated", nil
	case errors.Is(err, issue.ErrNotFound):
		return fmt.Sprintf("%s → %s (%s)", providerName, a.host(), a.email), nil
	case strings.Contains(err.Error(), "HTTP 401"),
		strings.Contains(err.Error(), "HTTP 403"):
		return "", fmt.Errorf("auth failed (check token and email)")
	default:
		return "", fmt.Errorf("connection failed: %w", err)
	}
}

// host returns the configured base URL reduced to its hostname, the
// form that reads best in a one-line doctor row.
func (a *Adapter) host() string {
	h := strings.TrimPrefix(a.baseURL, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimRight(h, "/")
}
