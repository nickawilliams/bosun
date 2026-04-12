package github

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// RepoIdentity holds the owner and name parsed from a git remote.
type RepoIdentity struct {
	Owner string
	Name  string
}

// ParseRemote extracts owner/repo from the origin remote URL of a git repo.
func ParseRemote(ctx context.Context, repoPath string) (RepoIdentity, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return RepoIdentity{}, fmt.Errorf("getting remote URL: %w", err)
	}
	return parseRemoteURL(strings.TrimSpace(string(out)))
}

// SSH: git@github.com:owner/repo.git
var sshPattern = regexp.MustCompile(`^git@[^:]+:([^/]+)/([^/]+?)(?:\.git)?$`)

// HTTPS: https://github.com/owner/repo.git
var httpsPattern = regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/]+?)(?:\.git)?$`)

// parseRemoteURL extracts owner/repo from a git remote URL string.
func parseRemoteURL(rawURL string) (RepoIdentity, error) {
	if m := sshPattern.FindStringSubmatch(rawURL); len(m) == 3 {
		return RepoIdentity{Owner: m[1], Name: m[2]}, nil
	}
	if m := httpsPattern.FindStringSubmatch(rawURL); len(m) == 3 {
		return RepoIdentity{Owner: m[1], Name: m[2]}, nil
	}
	return RepoIdentity{}, fmt.Errorf("cannot parse GitHub remote URL: %q", rawURL)
}
