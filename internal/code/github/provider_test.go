package github

import (
	"context"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

// stubConfig is a provider.Config over a map, recording Require calls so
// a test can tell "asked the user" from "found it itself".
type stubConfig struct {
	values   map[string]string
	required []string
}

func (c *stubConfig) Get(key string) string { return c.values[key] }

func (c *stubConfig) Require(keys ...string) error {
	c.required = append(c.required, keys...)
	return nil
}

func TestDescriptorURLs(t *testing.T) {
	a := New("token")
	repo := code.RepositoryIdentity{Owner: "acme", Name: "api"}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"repository", a.RepositoryURL(repo), "https://github.com/acme/api"},
		{"branch", a.BranchURL(repo, "feature/EX-1_x"), "https://github.com/acme/api/tree/feature/EX-1_x"},
		{"checks", a.ChecksURL(repo, "abc123"), "https://github.com/acme/api/commit/abc123/checks"},
		{"avatar", a.AvatarURL("octocat", 48), "https://github.com/octocat.png?size=48"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("= %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// TestWebURLsIgnoreAPIBaseURL pins that the web links are built from
// github.com regardless of the adapter's API endpoint. Every adapter in
// the test suite points at an httptest server; if the link builders
// followed baseURL, every URL a command rendered under test would carry a
// 127.0.0.1 host and no assertion would notice.
func TestWebURLsIgnoreAPIBaseURL(t *testing.T) {
	a := NewWithClient(nil, "http://127.0.0.1:8080", "token")
	repo := code.RepositoryIdentity{Owner: "acme", Name: "api"}

	if got := a.RepositoryURL(repo); got != "https://github.com/acme/api" {
		t.Errorf("RepositoryURL = %q, want the github.com web host", got)
	}
}

func TestDescriptorTokenResolution(t *testing.T) {
	t.Run("config token is used as-is", func(t *testing.T) {
		cfg := &stubConfig{values: map[string]string{
			code.ConfigGroup + ".token": "from-config",
		}}

		host, err := Descriptor().New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		a, ok := host.(*Adapter)
		if !ok {
			t.Fatalf("New returned %T, want *Adapter", host)
		}
		if a.token != "from-config" {
			t.Errorf("token = %q, want the configured one", a.token)
		}
		// A configured token is the whole answer — nothing to prompt for.
		if len(cfg.required) != 0 {
			t.Errorf("Require calls = %v, want none", cfg.required)
		}
	})

	t.Run("no config and no discovery prompts, then reads what landed", func(t *testing.T) {
		// Neuter ResolveToken's two sources so the prompt arm is the one
		// that runs: an empty PATH means no gh binary to shell out to, and
		// GITHUB_TOKEN is the fallback it reads directly.
		withoutTokenDiscovery(t)

		cfg := &promptingConfig{stubConfig: stubConfig{values: map[string]string{}},
			onRequire: map[string]string{code.ConfigGroup + ".token": "from-prompt"}}

		host, err := Descriptor().New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		a, ok := host.(*Adapter)
		if !ok {
			t.Fatalf("New returned %T, want *Adapter", host)
		}
		if a.token != "from-prompt" {
			t.Errorf("token = %q, want the prompted one", a.token)
		}
		if len(cfg.required) != 1 || cfg.required[0] != code.ConfigGroup {
			t.Errorf("Require calls = %v, want [%s]", cfg.required, code.ConfigGroup)
		}
	})

	t.Run("discovery is tried before prompting", func(t *testing.T) {
		// GITHUB_TOKEN is ResolveToken's second source; finding a token
		// there must settle it without asking the user anything.
		withoutTokenDiscovery(t)
		t.Setenv("GITHUB_TOKEN", "from-env")

		cfg := &stubConfig{values: map[string]string{}}

		host, err := Descriptor().New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if a := host.(*Adapter); a.token != "from-env" {
			t.Errorf("token = %q, want the discovered one", a.token)
		}
		if len(cfg.required) != 0 {
			t.Errorf("Require calls = %v, want none — discovery answered it", cfg.required)
		}
	})
}

// withoutTokenDiscovery makes ResolveToken come up empty regardless of how
// the machine running the test is set up: an empty PATH hides the gh CLI,
// and an empty GITHUB_TOKEN closes its other source.
func withoutTokenDiscovery(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
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

func TestAdapterParseRemote(t *testing.T) {
	// The adapter reads remotes with the shared implementation, so what
	// this pins is the delegation: a path with no git remote fails rather
	// than returning a zero identity that later reads as owner "".
	a := New("token")

	if _, err := a.ParseRemote(context.Background(), t.TempDir()); err == nil {
		t.Error("ParseRemote succeeded for a directory that isn't a repository")
	}
}

func TestDescriptorShape(t *testing.T) {
	d := Descriptor()

	if d.Name != "github" {
		t.Errorf("Name = %q, want %q", d.Name, "github")
	}
	if len(d.Keys) != 1 || d.Keys[0].Key != "token" {
		t.Fatalf("Keys = %+v, want just the token", d.Keys)
	}
	if !d.Keys[0].Secret {
		t.Error("token key is not marked Secret")
	}
	if d.Keys[0].EnvVar != "GITHUB_TOKEN" {
		t.Errorf("token EnvVar = %q, want GITHUB_TOKEN", d.Keys[0].EnvVar)
	}
}
