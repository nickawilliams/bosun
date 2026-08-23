package ephemeral

import (
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in
// preview.provider.
const providerName = "ephemeral"

// authModeGitHubCLI is the only supported auth mode. It is a config key
// with one option rather than no key at all so that adding a second
// mode later doesn't change the shape of an existing config.
const authModeGitHubCLI = "gh-cli"

// Config keys this provider contributes to the "preview" group.
const (
	keyBaseURL = "api.base_url"
	keyAuth    = "api.auth"
)

// Descriptor registers the HTTP adapter with the services registry.
func Descriptor() preview.ProviderDescriptor {
	return preview.ProviderDescriptor{
		Name: providerName,
		Keys: []provider.ConfigKey{
			{
				Key:      keyBaseURL,
				Label:    "API base URL",
				Example:  "https://ephemeral-ui.example.dev",
				Required: true,
			},
			{
				Key:     keyAuth,
				Label:   "API auth mode",
				Options: []string{authModeGitHubCLI},
				Default: authModeGitHubCLI,
			},
		},
		New: func(cfg provider.Config, deps preview.Deps) (preview.Provider, error) {
			// The base URL is the one value with no discoverable
			// default — prompted for on a TTY, an error otherwise. The
			// token needs no prompt: it comes from the GitHub CLI the
			// developer has already authenticated.
			if err := cfg.Require(fullKey(keyBaseURL)); err != nil {
				return nil, err
			}
			return New(Options{
				BaseURL:     cfg.Get(fullKey(keyBaseURL)),
				Tracker:     deps.Tracker,
				URLTemplate: deps.URLTemplate,
			}), nil
		},
	}
}

// fullKey renders a provider-relative key as the absolute one
// provider.Config reads.
func fullKey(key string) string { return preview.ConfigGroup + "." + key }
