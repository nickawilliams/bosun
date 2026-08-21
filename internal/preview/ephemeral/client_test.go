package ephemeral

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/nickawilliams/bosun/internal/preview"
)

// TestAPIErrorMessage pins which of the API's three error fields is
// shown. Details is the most specific — "Single words are not allowed"
// rather than "Invalid ephemeral name" — so it wins.
func TestAPIErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		env  apiError
		want string
	}{
		{"details preferred", apiError{Error: "Invalid name", Message: "m", Details: "d"}, "d"},
		{"message next", apiError{Error: "Invalid name", Message: "m"}, "m"},
		{"error last", apiError{Error: "Invalid name"}, "Invalid name"},
		{"all empty", apiError{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.env.message(); got != tc.want {
				t.Errorf("message() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStatusErrorMessage(t *testing.T) {
	withBody := &statusError{Code: 400, Body: "bad name", URL: "https://x/api/deploy"}
	if got := withBody.Error(); !strings.Contains(got, "400") || !strings.Contains(got, "bad name") {
		t.Errorf("Error() = %q, want the code and the reason", got)
	}
	// A body-less failure still has to name the endpoint and the code;
	// "HTTP 502" with no URL says nothing about what was called.
	bare := &statusError{Code: 502, URL: "https://x/api/deployments"}
	got := bare.Error()
	if !strings.Contains(got, "502") || !strings.Contains(got, "/api/deployments") {
		t.Errorf("Error() = %q, want the code and the endpoint", got)
	}
}

func TestStatusErrorRetryable(t *testing.T) {
	// 4xx is the server's considered answer; retrying it just delays the
	// report. 5xx means the server is there but not answering usefully.
	for code, want := range map[int]bool{400: false, 401: false, 404: false, 500: true, 502: true, 503: true} {
		if got := (&statusError{Code: code}).retryable(); got != want {
			t.Errorf("statusError{%d}.retryable() = %v, want %v", code, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	if got := describe([]byte(`{"details":"nope"}`), "500 Internal Server Error"); got != "nope" {
		t.Errorf("describe = %q, want the API's own text", got)
	}
	// Non-JSON short bodies pass through — a reverse proxy's plain-text
	// error is still the most useful thing to show.
	if got := describe([]byte("upstream timed out"), "504 Gateway Timeout"); got != "upstream timed out" {
		t.Errorf("describe = %q, want the raw body", got)
	}
	// A long body is the wrong thing in a one-line error: an HTML error
	// page would bury the status it is reporting.
	long := strings.Repeat("x", 500)
	if got := describe([]byte(long), "502 Bad Gateway"); got != "502 Bad Gateway" {
		t.Errorf("describe = %q, want the status line for an oversized body", got)
	}
	if got := describe(nil, "503 Service Unavailable"); got != "503 Service Unavailable" {
		t.Errorf("describe = %q, want the status line for an empty body", got)
	}
}

func TestIndeterminate(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":  {nil, false},
		"auth": {preview.ErrAuth, false},
		// Wrapped, not string-matched: the adapter always annotates the
		// sentinel with the API's reason before returning it.
		"wrapped auth":      {fmt.Errorf("%w: expired", preview.ErrAuth), false},
		"not configured":    {preview.ErrNotConfigured, false},
		"client error":      {&statusError{Code: 404}, false},
		"server error":      {&statusError{Code: 503}, true},
		"transport failure": {errors.New("dial tcp: connection refused"), true},
		"decode failure":    {errors.New("decoding response: unexpected EOF"), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := indeterminate(tc.err); got != tc.want {
				t.Errorf("indeterminate(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestDeployments_CancelledContextSkipsTheRetry pins the guard on the
// retry loop. A caller whose deadline is already gone gets its answer
// now; a second attempt would fail identically and only delay the
// report.
func TestDeployments_CancelledContextSkipsTheRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			// Cancel mid-flight so the loop sees a dead context after a
			// failure that would otherwise be retried.
			cancel()
			w.WriteHeader(http.StatusBadGateway)
		}).build(t)

	_, err := p.Get(ctx, "PROJ-1")
	var pe *preview.ProbeError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want *preview.ProbeError", err)
	}
	if calls != 1 {
		t.Errorf("made %d attempts, want 1 — the retry ran against a dead context", calls)
	}
}

// TestRenderURLTemplateFailure pins the fallback for a template that
// parses but can't execute. Returning a half-rendered URL would be
// worse than returning none: the caller links it.
func TestRenderURLTemplateFailure(t *testing.T) {
	broken := template.Must(template.New("u").Parse("https://{{.Name.Nope}}.example.dev"))
	p, _ := newBuilder().
		store_(bound("brave-falcon")).
		handle(func(w http.ResponseWriter, _ *http.Request) {
			writeDeployments(w, entry("brave-falcon", "active"))
		}).build(t)
	a := p.(*adapter)
	a.urlTemplate = broken

	env, err := a.Get(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// With no renderable template the API's own URL is the fallback.
	if want := "https://api-said-brave-falcon.example.dev"; env.URL != want {
		t.Errorf("URL = %q, want the API's %q", env.URL, want)
	}

	// And an empty name never renders, template or not.
	if got := a.renderURL(""); got != "" {
		t.Errorf("renderURL(\"\") = %q, want empty", got)
	}
}

func TestInspect_AuthFailureCarriesTheName(t *testing.T) {
	p, _ := newBuilder().
		withTemplate().
		handle(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"Not a member of the org"}`))
		}).build(t)

	env, err := p.Inspect(context.Background(), "brave-falcon")
	// 403 is an authorization refusal, not a transport fault: the token
	// is real but not entitled, and retrying won't change that.
	if !errors.Is(err, preview.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "Not a member of the org") {
		t.Errorf("err = %v, want the API's explanation", err)
	}
	if env.Name != "brave-falcon" || env.Probed {
		t.Errorf("env = %+v, want the name back and Probed false", env)
	}
}

// TestGitHubCLIToken_PrefersTheCLI covers the primary credential path
// with a stand-in `gh` on PATH. The real one reads a local credential
// store, so there is nothing to talk to and nothing to mock beyond the
// executable itself.
func TestGitHubCLIToken_PrefersTheCLI(t *testing.T) {
	dir := t.TempDir()
	writeFakeGH(t, dir, "#!/bin/sh\necho cli-token\n")
	t.Setenv("PATH", dir)
	// Set so a fallback would be visible as the wrong answer rather than
	// as an error.
	t.Setenv("GITHUB_TOKEN", "env-token")

	got, err := GitHubCLIToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "cli-token" {
		t.Errorf("token = %q, want cli-token — the CLI is the preferred source", got)
	}
}

// TestGitHubCLIToken_FallsBackWhenTheCLIFails covers the two ways gh can
// be present but useless: a non-zero exit (not logged in) and an empty
// answer.
func TestGitHubCLIToken_FallsBackWhenTheCLIFails(t *testing.T) {
	cases := map[string]string{
		"gh exits non-zero": "#!/bin/sh\necho 'not logged in' >&2\nexit 1\n",
		"gh prints nothing": "#!/bin/sh\necho ''\n",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFakeGH(t, dir, script)
			t.Setenv("PATH", dir)
			t.Setenv("GITHUB_TOKEN", "env-token")

			got, err := GitHubCLIToken(context.Background())
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != "env-token" {
				t.Errorf("token = %q, want env-token", got)
			}
		})
	}
}

// TestGitHubCLIToken_TrimsWhitespace pins that the newline gh prints
// doesn't reach the Authorization header, where it would be rejected as
// a malformed value rather than a bad token.
func TestGitHubCLIToken_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	writeFakeGH(t, dir, "#!/bin/sh\nprintf '  padded-token \\n'\n")
	t.Setenv("PATH", dir)
	t.Setenv("GITHUB_TOKEN", "")

	got, err := GitHubCLIToken(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "padded-token" {
		t.Errorf("token = %q, want it trimmed", got)
	}
}

// writeFakeGH drops an executable `gh` into dir.
func writeFakeGH(t *testing.T, dir, script string) {
	t.Helper()
	path := filepath.Join(dir, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake gh: %v", err)
	}
}
