package githubactions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

// stubConfig is a provider.Config over a map, recording Require calls.
type stubConfig struct {
	values   map[string]string
	required []string
}

func (c *stubConfig) Get(key string) string { return c.values[key] }

func (c *stubConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	return nil
}

func TestDescriptorShape(t *testing.T) {
	d := Descriptor()

	if d.Name != "github_actions" {
		t.Errorf("Name = %q, want %q", d.Name, "github_actions")
	}
	// No keys of its own: the pipeline authenticates with the code host's
	// token, which is the config group it reads from below.
	if len(d.Keys) != 0 {
		t.Errorf("Keys = %+v, want none", d.Keys)
	}
}

// TestDescriptorBorrowsTheCodeHostToken pins the credential coupling the
// adapter owns: workflow dispatch runs against the same GitHub account as
// code hosting, so the token comes from the code_host group. The coupling
// is deliberate; what matters is that it lives here rather than in a
// caller reaching across two capabilities.
func TestDescriptorBorrowsTheCodeHostToken(t *testing.T) {
	cfg := &stubConfig{values: map[string]string{
		code.ConfigGroup + ".token": "shared-token",
	}}

	pipeline, err := Descriptor().New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, ok := pipeline.(*Adapter)
	if !ok {
		t.Fatalf("New returned %T, want *Adapter", pipeline)
	}
	if a.token != "shared-token" {
		t.Errorf("token = %q, want the code host's", a.token)
	}
	if len(cfg.required) != 0 {
		t.Errorf("Require calls = %v, want none — the token was already there", cfg.required)
	}
}

// TestDescriptorPromptsForTheCodeHostGroup covers the last resort: no
// token in config, none discoverable, so it asks the config layer to
// complete the code host's group — not its own, which has no token key.
func TestDescriptorPromptsForTheCodeHostGroup(t *testing.T) {
	// Neuter the discovery path so the prompt arm is the one that runs:
	// an empty PATH hides the gh CLI, and GITHUB_TOKEN is its other source.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")

	cfg := &promptingConfig{
		stubConfig: stubConfig{values: map[string]string{}},
		onRequire:  map[string]string{code.ConfigGroup + ".token": "from-prompt"},
	}

	pipeline, err := Descriptor().New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a := pipeline.(*Adapter); a.token != "from-prompt" {
		t.Errorf("token = %q, want the prompted one", a.token)
	}
	if len(cfg.required) != 1 || cfg.required[0] != code.ConfigGroup {
		t.Errorf("Require calls = %v, want [%s]", cfg.required, code.ConfigGroup)
	}
}

// promptingConfig is a stubConfig whose Require fills in values, standing
// in for the CLI prompting the user and persisting the answer.
type promptingConfig struct {
	stubConfig
	onRequire map[string]string
}

func (c *promptingConfig) Require(keys ...string) error {
	if err := c.stubConfig.Require(keys...); err != nil {
		return err
	}
	for k, v := range c.onRequire {
		c.values[k] = v
	}
	return nil
}

func TestAuthTest(t *testing.T) {
	t.Run("reports the pipeline identity", func(t *testing.T) {
		var gotPath, gotAuth string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"login":"octocat"}`))
		}))
		defer server.Close()

		a := NewWithClient(server.Client(), server.URL, "token")
		got, err := a.AuthTest(context.Background())
		if err != nil {
			t.Fatalf("AuthTest: %v", err)
		}

		if got != "github actions → octocat" {
			t.Errorf("AuthTest() = %q, want %q", got, "github actions → octocat")
		}
		if gotPath != "/user" {
			t.Errorf("probe path = %q, want /user", gotPath)
		}
		// The probe must carry the pipeline's own credentials — a
		// successful unauthenticated call would prove nothing.
		if gotAuth != "Bearer token" {
			t.Errorf("Authorization = %q, want the bearer token", gotAuth)
		}
	})

	t.Run("surfaces a rejected token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		}))
		defer server.Close()

		a := NewWithClient(server.Client(), server.URL, "bad")
		got, err := a.AuthTest(context.Background())
		if err == nil {
			t.Fatalf("AuthTest() = %q, want an error", got)
		}
		if !strings.Contains(err.Error(), "401") {
			t.Errorf("AuthTest() error = %v, want it to name the status", err)
		}
	})
}
