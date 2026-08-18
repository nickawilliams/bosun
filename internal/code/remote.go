package code

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ParseRemote resolves a local clone's host identity from its origin
// remote. It is the default implementation of Host.ParseRemote, which
// every current host uses as-is.
//
// It is exported because a repository's identity is knowable from the
// clone alone, with no credentials and no host object — and two callers
// need it in exactly that position, resolving CI/CD workflow targets
// while the host-backed services are still being constructed. Callers
// that already hold a Host should go through the interface so a host
// that reads remotes differently (nested GitLab groups, a self-hosted
// domain) can say so.
func ParseRemote(ctx context.Context, repositoryPath string) (RepositoryIdentity, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = repositoryPath
	out, err := cmd.Output()
	if err != nil {
		return RepositoryIdentity{}, fmt.Errorf("getting remote URL: %w", err)
	}
	return parseRemoteURL(strings.TrimSpace(string(out)))
}

// SSH: git@github.com:owner/repository.git
var sshPattern = regexp.MustCompile(`^git@[^:]+:([^/]+)/([^/]+?)(?:\.git)?$`)

// HTTPS: https://github.com/owner/repository.git
var httpsPattern = regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/]+?)(?:\.git)?$`)

// parseRemoteURL extracts owner/repository from a git remote URL string.
func parseRemoteURL(rawURL string) (RepositoryIdentity, error) {
	if m := sshPattern.FindStringSubmatch(rawURL); len(m) == 3 {
		return RepositoryIdentity{Owner: m[1], Name: m[2]}, nil
	}
	if m := httpsPattern.FindStringSubmatch(rawURL); len(m) == 3 {
		return RepositoryIdentity{Owner: m[1], Name: m[2]}, nil
	}
	return RepositoryIdentity{}, fmt.Errorf("cannot parse git remote URL: %q", rawURL)
}
