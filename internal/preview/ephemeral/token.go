package ephemeral

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nickawilliams/bosun/internal/preview"
)

// tokenTimeout bounds the shell-out to the GitHub CLI. `gh auth token`
// reads a local credential store, so anything slower than this means gh
// is wedged rather than working.
const tokenTimeout = 2 * time.Second

// GitHubCLIToken resolves a GitHub token from the GitHub CLI, falling
// back to GITHUB_TOKEN.
//
// The API authenticates with a GitHub OAuth bearer and checks org
// membership server-side, so the token a developer already has from
// `gh auth login` is exactly the credential it wants — and bosun
// already requires gh elsewhere. Refresh is gh's problem: an expired
// token surfaces as preview.ErrAuth and the CLI says to log in again,
// rather than this package growing a refresh loop of its own.
func GitHubCLIToken(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err == nil {
		tctx, cancel := context.WithTimeout(ctx, tokenTimeout)
		defer cancel()
		out, err := exec.CommandContext(tctx, "gh", "auth", "token").Output()
		if err == nil {
			if token := strings.TrimSpace(string(out)); token != "" {
				return token, nil
			}
		}
	}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("%w: no GitHub token from `gh auth token` or GITHUB_TOKEN", preview.ErrAuth)
}
