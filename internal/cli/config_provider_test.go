package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/spf13/viper"
)

// TestProviderConfigGet pins the read side: adapters see viper's merged
// view (config file, env, anything a prior Require materialized).
func TestProviderConfigGet(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	viper.Set("issue_tracker.base_url", "https://acme.atlassian.net")

	cfg := providerConfig{}
	if got := cfg.Get("issue_tracker.base_url"); got != "https://acme.atlassian.net" {
		t.Errorf("Get = %q, want the configured value", got)
	}
	if got := cfg.Get("issue_tracker.nothing_here"); got != "" {
		t.Errorf("Get for an unset key = %q, want empty", got)
	}
}

// TestProviderConfigRequire pins the write side of the boundary: Require
// runs bosun's just-in-time config resolution, so a value an adapter asks
// for and doesn't have becomes available through Get afterward. Exercised
// with a schema key carrying a default, which resolves without a terminal.
func TestProviderConfigRequire(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	cfg := providerConfig{}
	if err := cfg.Require("ui.color"); err != nil {
		t.Fatalf("Require: %v", err)
	}
	if got := cfg.Get("ui.color"); got == "" {
		t.Error("Require returned nil but left the value unset")
	}
}

// TestProviderConfigRequireReportsUnresolvable pins the error path: a
// required key with no default, no env var, and no terminal to prompt at
// must fail rather than silently hand the adapter an empty string.
func TestProviderConfigRequireReportsUnresolvable(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	err := providerConfig{}.Require("bosun_key_that_does_not_exist")
	if err == nil {
		t.Fatal("Require succeeded for an unresolvable key")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want it to say the key isn't configured", err)
	}
}

// TestDefaultServicesDelegateToTheRegistry pins that the production
// factory set is wired to services rather than to anything cli builds
// itself. Notifier is the deterministic probe: an unset provider is the
// registry's own opt-in error, and no other layer produces that message.
func TestDefaultServicesDelegateToTheRegistry(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()

	svcs := DefaultServices()

	t.Run("notifier", func(t *testing.T) {
		_, err := svcs.Notifier()
		if err == nil {
			t.Fatal("Notifier succeeded with no provider configured")
		}
		if !strings.Contains(err.Error(), "notification provider not configured") {
			t.Errorf("error = %v, want the registry's opt-in error", err)
		}
	})

	t.Run("issue tracker", func(t *testing.T) {
		// No provider set and no terminal to ask at, so the provider key
		// can't be resolved — the registry resolves it first for exactly
		// this reason.
		if _, err := svcs.IssueTracker(); err == nil {
			t.Error("IssueTracker succeeded with nothing configured")
		}
	})

	t.Run("code host", func(t *testing.T) {
		// The host is the lax one: an unset provider falls back to the
		// sole registered host, which then discovers credentials itself.
		// Whether that discovery finds anything depends on the machine,
		// so this asserts only that it went somewhere — a nil host with a
		// nil error would be the bug.
		host, err := svcs.CodeHost()
		if err == nil && host == nil {
			t.Error("CodeHost returned nil, nil")
		}
	})

	t.Run("cicd", func(t *testing.T) {
		pipeline, err := svcs.CICD()
		if err == nil && pipeline == nil {
			t.Error("CICD returned nil, nil")
		}
	})
}

// TestHostURLHelpersTolerateNoHost pins the degradation the lifecycle
// commands rely on: with no code host configured, a notification still
// goes out and a card still renders — just without links.
func TestHostURLHelpersTolerateNoHost(t *testing.T) {
	if got := avatarURL(nil, "octocat"); got != "" {
		t.Errorf("avatarURL(nil, …) = %q, want empty", got)
	}
	if got := branchURL(nil, "acme", "api", "main"); got != "" {
		t.Errorf("branchURL(nil, …) = %q, want empty", got)
	}
}

// TestAvatarURLRequiresALogin covers the other empty case: the
// reviewer-exclusion pass may not have resolved a login, and asking the
// host for the avatar of "" would produce a broken image URL.
func TestAvatarURLRequiresALogin(t *testing.T) {
	if got := avatarURL(urlOnlyHost{}, ""); got != "" {
		t.Errorf("avatarURL(host, \"\") = %q, want empty", got)
	}
}

// TestHostURLHelpersUseTheHost pins that the helpers ask the host for the
// URL shape rather than formatting one themselves, and that the shared
// card-icon size is what reaches AvatarURL.
func TestHostURLHelpersUseTheHost(t *testing.T) {
	host := urlOnlyHost{}

	if got, want := avatarURL(host, "octocat"), "avatar:octocat@48"; got != want {
		t.Errorf("avatarURL = %q, want %q", got, want)
	}
	if got, want := branchURL(host, "acme", "api", "main"), "branch:acme/api@main"; got != want {
		t.Errorf("branchURL = %q, want %q", got, want)
	}
}

// urlOnlyHost implements just the link surface, with shapes no real host
// would produce so a test can tell "went through the host" from
// "formatted a github.com URL locally". Everything else panics via the
// embedded nil interface.
type urlOnlyHost struct {
	code.Host
}

func (urlOnlyHost) AvatarURL(login string, size int) string {
	return "avatar:" + login + "@" + strconv.Itoa(size)
}

func (urlOnlyHost) BranchURL(repo code.RepositoryIdentity, branch string) string {
	return "branch:" + repo.Owner + "/" + repo.Name + "@" + branch
}

func (urlOnlyHost) ParseRemote(context.Context, string) (code.RepositoryIdentity, error) {
	return code.RepositoryIdentity{Owner: "from", Name: "host"}, nil
}

// TestRepoIdentityPrefersTheHost pins the routing: with a host in hand,
// identity comes from the host so one that reads remotes differently gets
// a say.
func TestRepoIdentityPrefersTheHost(t *testing.T) {
	got, err := repoIdentity(context.Background(), urlOnlyHost{}, t.TempDir())
	if err != nil {
		t.Fatalf("repoIdentity: %v", err)
	}
	if got.Owner != "from" || got.Name != "host" {
		t.Errorf("identity = %+v, want the host's answer", got)
	}
}

// TestRepoIdentityFallsBackWithoutAHost pins the other arm: review and
// prerelease resolve identities even when the host is unreachable, so a
// nil host reads the remote directly rather than panicking. A scratch
// directory has no remote, so what's asserted is that it got as far as
// trying.
func TestRepoIdentityFallsBackWithoutAHost(t *testing.T) {
	if _, err := repoIdentity(context.Background(), nil, t.TempDir()); err == nil {
		t.Error("repoIdentity succeeded for a directory that isn't a repository")
	}
}
