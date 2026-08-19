package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/provider"
)

func TestParseIdentifier(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare key", "PROJ-123", "PROJ-123"},
		{"branch name", "feature/PROJ-123_add-widget", "PROJ-123"},
		{"workspace path", "fix/CS-42_bug", "CS-42"},
		{"digits in project key", "A1B2-7", "A1B2-7"},
		{"commit subject", "PROJ-9: tighten the gate", "PROJ-9"},
		{"lowercase is not a key", "feature/proj-123", ""},
		{"single-letter project key", "P-1", ""},
		{"no number", "PROJ-", ""},
		{"nothing at all", "main", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseIdentifier(tt.in); got != tt.want {
				t.Errorf("ParseIdentifier(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// stubConfig is a provider.Config over a map. requireErr is what Require
// returns, standing in for the CLI's just-in-time prompt failing.
type stubConfig struct {
	values     map[string]string
	requireErr error
	required   []string
}

func (c *stubConfig) Get(key string) string { return c.values[key] }

func (c *stubConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	return c.requireErr
}

func TestDescriptor(t *testing.T) {
	d := Descriptor()

	if d.Name != "jira" {
		t.Errorf("Name = %q, want %q", d.Name, "jira")
	}
	if d.ParseIdentifier == nil {
		t.Fatal("ParseIdentifier is nil — the CLI parses keys through the descriptor")
	}
	if got := d.ParseIdentifier("feature/PROJ-1_x"); got != "PROJ-1" {
		t.Errorf("descriptor ParseIdentifier = %q, want %q", got, "PROJ-1")
	}

	// Every key the adapter needs is declared, so the config schema can
	// prompt for them without knowing they're Jira's.
	want := map[string]bool{"base_url": true, "email": true, "token": true}
	for _, ck := range d.Keys {
		delete(want, ck.Key)
	}
	if len(want) > 0 {
		t.Errorf("descriptor is missing keys %v", want)
	}
}

func TestDescriptorNew(t *testing.T) {
	t.Run("builds from config", func(t *testing.T) {
		cfg := &stubConfig{values: map[string]string{
			issue.ConfigGroup + ".base_url": "https://acme.atlassian.net/",
			issue.ConfigGroup + ".email":    "dev@acme.test",
			issue.ConfigGroup + ".token":    "secret",
		}}

		tracker, err := Descriptor().New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		a, ok := tracker.(*Adapter)
		if !ok {
			t.Fatalf("New returned %T, want *Adapter", tracker)
		}
		if a.baseURL != "https://acme.atlassian.net" {
			t.Errorf("baseURL = %q, want the trailing slash trimmed", a.baseURL)
		}
		if a.email != "dev@acme.test" || a.token != "secret" {
			t.Errorf("credentials = %q/%q, want dev@acme.test/secret", a.email, a.token)
		}
		// The adapter has no credential discovery of its own, so it asks
		// the config layer to complete the group before reading it.
		if len(cfg.required) != 1 || cfg.required[0] != issue.ConfigGroup {
			t.Errorf("Require calls = %v, want [%s]", cfg.required, issue.ConfigGroup)
		}
	})

	t.Run("propagates an incomplete config", func(t *testing.T) {
		cfg := &stubConfig{requireErr: errNotConfigured}

		if _, err := Descriptor().New(cfg); err != errNotConfigured {
			t.Errorf("New error = %v, want the Require error verbatim", err)
		}
	})
}

// errNotConfigured stands in for whatever the CLI's config layer returns
// when a required value can't be resolved.
var errNotConfigured = errTest("not configured")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestAuthTest(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		want    string
		wantErr string
	}{
		{
			name:   "success reports authenticated",
			status: http.StatusOK,
			want:   "jira · authenticated",
		},
		{
			name:   "404 reports the endpoint identity",
			status: http.StatusNotFound,
			want:   "jira → %s (dev@acme.test)",
		},
		{
			name:    "401 reads as an auth problem",
			status:  http.StatusUnauthorized,
			wantErr: "auth failed (check token and email)",
		},
		{
			name:    "403 reads as an auth problem",
			status:  http.StatusForbidden,
			wantErr: "auth failed (check token and email)",
		},
		{
			name:    "500 reads as a connection problem",
			status:  http.StatusInternalServerError,
			wantErr: "connection failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if tt.status != http.StatusOK {
					w.WriteHeader(tt.status)
					return
				}
				_, _ = w.Write([]byte(`{"key":"BOSUN-0","fields":{"summary":"probe"}}`))
			}))
			defer server.Close()

			a := NewWithClient(server.Client(), server.URL, "dev@acme.test", "token")
			got, err := a.AuthTest(context.Background())

			if !strings.Contains(gotPath, probeKey) {
				t.Errorf("probe path = %q, want it to fetch %s", gotPath, probeKey)
			}

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("AuthTest() = %q, want error containing %q", got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("AuthTest() error = %v, want it to contain %q", err, tt.wantErr)
				}
				if got != "" {
					t.Errorf("AuthTest() = %q on error, want empty", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("AuthTest() error: %v", err)
			}
			// The identity line shows the host, not the scheme-bearing
			// base URL the adapter was built with.
			want := strings.Replace(tt.want, "%s", strings.TrimPrefix(server.URL, "http://"), 1)
			if got != want {
				t.Errorf("AuthTest() = %q, want %q", got, want)
			}
		})
	}
}

// TestDescriptorConfigKeysAreProviderShaped pins the two facts the config
// schema depends on: the token is a secret sourced from an env var, and
// the connection keys are required. Without them, `bosun init` would
// write a token into project YAML or let a half-configured tracker pass
// `config check`.
func TestDescriptorConfigKeysAreProviderShaped(t *testing.T) {
	byKey := map[string]provider.ConfigKey{}
	for _, ck := range Descriptor().Keys {
		byKey[ck.Key] = ck
	}

	token := byKey["token"]
	if !token.Secret {
		t.Error("token key is not marked Secret")
	}
	if token.EnvVar != "BOSUN_JIRA_TOKEN" {
		t.Errorf("token EnvVar = %q, want BOSUN_JIRA_TOKEN", token.EnvVar)
	}
	for _, key := range []string{"base_url", "email", "token"} {
		if !byKey[key].Required {
			t.Errorf("%s key is not marked Required", key)
		}
	}
}
