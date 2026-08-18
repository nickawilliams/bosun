package githubactions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/provider"
)

// providerName is the value that selects this adapter in cicd.provider,
// and the name it goes by in doctor rows.
const providerName = "github_actions"

// Descriptor registers the GitHub Actions adapter with the services
// registry.
//
// It contributes no config keys of its own: workflow dispatch runs
// against the same GitHub account as the code host, so it reads that
// group's token rather than asking for a second one. That's the honest
// model — pipelines that authenticate independently would declare their
// own keys here.
func Descriptor() cicd.Descriptor {
	return cicd.Descriptor{
		Name: providerName,
		New: func(cfg provider.Config) (cicd.CICD, error) {
			if token := cfg.Get(code.ConfigGroup + ".token"); token != "" {
				return New(token), nil
			}
			if token := github.ResolveToken(); token != "" {
				return New(token), nil
			}
			if err := cfg.Require(code.ConfigGroup); err != nil {
				return nil, err
			}
			return New(cfg.Get(code.ConfigGroup + ".token")), nil
		},
	}
}

// AuthTest verifies the token by asking GitHub who it belongs to, and
// reports the pipeline's identity for a doctor row.
func (a *Adapter) AuthTest(ctx context.Context) (string, error) {
	resp, err := a.doRequest(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing user response: %w", err)
	}
	return fmt.Sprintf("github actions → %s", result.Login), nil
}
