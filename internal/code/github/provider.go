package github

import (
	"context"
	"fmt"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in
// code_host.provider.
const providerName = "github"

// webHost is the origin GitHub serves its web pages from. It is
// separate from the adapter's API base URL (api.github.com, or a test
// server): the URLs built here are for humans to click, not for the
// adapter to call, so they don't move when the API endpoint does.
const webHost = "https://github.com"

// Descriptor registers the GitHub adapter with the services registry:
// the config it needs, and how to build it from that config.
func Descriptor() code.HostDescriptor {
	return code.HostDescriptor{
		Name: providerName,
		Keys: []provider.ConfigKey{
			{Key: "token", Label: "personal access token", EnvVar: "GITHUB_TOKEN", Secret: true, Required: true},
		},
		New: func(cfg provider.Config) (code.Host, error) {
			// Config first (file, or GITHUB_TOKEN via the CLI's env
			// binding), then the gh CLI's own credentials, and only then
			// prompt: a developer with `gh auth login` already done
			// should never be asked for a token.
			if token := cfg.Get(code.ConfigGroup + ".token"); token != "" {
				return New(token), nil
			}
			if token := ResolveToken(); token != "" {
				return New(token), nil
			}
			if err := cfg.Require(code.ConfigGroup); err != nil {
				return nil, err
			}
			return New(cfg.Get(code.ConfigGroup + ".token")), nil
		},
	}
}

// ParseRemote resolves the GitHub identity of a local clone. GitHub's
// remote URLs are the plain owner/name shape code.ParseRemote already
// handles, so the adapter adds nothing of its own.
func (a *Adapter) ParseRemote(ctx context.Context, repositoryPath string) (code.RepositoryIdentity, error) {
	return code.ParseRemote(ctx, repositoryPath)
}

// RepositoryURL returns the repository's web page.
func (a *Adapter) RepositoryURL(repo code.RepositoryIdentity) string {
	return fmt.Sprintf("%s/%s/%s", webHost, repo.Owner, repo.Name)
}

// BranchURL returns the web page showing a branch's tree.
func (a *Adapter) BranchURL(repo code.RepositoryIdentity, branch string) string {
	return fmt.Sprintf("%s/%s/%s/tree/%s", webHost, repo.Owner, repo.Name, branch)
}

// ChecksURL returns the checks tab of a commit ref.
func (a *Adapter) ChecksURL(repo code.RepositoryIdentity, ref string) string {
	return fmt.Sprintf("%s/%s/%s/commit/%s/checks", webHost, repo.Owner, repo.Name, ref)
}

// AvatarURL returns a login's avatar image at the requested pixel size.
func (a *Adapter) AvatarURL(login string, size int) string {
	return fmt.Sprintf("%s/%s.png?size=%d", webHost, login, size)
}
