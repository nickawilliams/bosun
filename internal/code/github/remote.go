package github

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// RepositoryIdentity holds the owner and name parsed from a git remote.
type RepositoryIdentity struct {
	Owner string
	Name  string
}

// ParseRemote extracts owner/repository from the origin remote URL of a git repository.
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
	return RepositoryIdentity{}, fmt.Errorf("cannot parse GitHub remote URL: %q", rawURL)
}
