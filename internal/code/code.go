package code

import "context"

// PullRequest represents a pull request on a code hosting platform.
type PullRequest struct {
	Number int
	Title  string
	URL    string
	State  string // "open", "closed", "merged"
	Review string // "approved", "changes_requested", "pending", ""
}

// CreatePRRequest holds the fields needed to create a pull request.
type CreatePRRequest struct {
	Owner      string // Repository owner (org or user)
	Repository string // Repository name
	Head       string // Source branch
	Base       string // Target branch (e.g., "main")
	Title      string
	Body       string
	Draft      bool
}

// Release represents a release/tag on a code hosting platform.
type Release struct {
	Tag string // e.g., "v1.2.3"
	URL string
}

// CreateReleaseRequest holds the fields needed to create a release.
type CreateReleaseRequest struct {
	Owner      string
	Repository string
	Tag        string // e.g., "v1.2.3"
	Target     string // Branch or commit SHA to tag
	Name       string // Release title
	Body       string // Release notes
}

// Host defines code hosting operations needed by bosun.
type Host interface {
	// CreatePR creates a pull request. If a PR already exists for the
	// head branch, it returns the existing PR (idempotent).
	CreatePR(ctx context.Context, req CreatePRRequest) (PullRequest, error)

	// GetPRForBranch returns the PR for a given head branch. Returns a
	// PullRequest with Number==0 if none exists.
	GetPRForBranch(ctx context.Context, owner, repository, branch string) (PullRequest, error)

	// RequestReviewers requests reviews from the given users and/or teams on a PR.
	RequestReviewers(ctx context.Context, owner, repository string, number int, reviewers, teamReviewers []string) error

	// AddAssignees adds assignees to a pull request.
	AddAssignees(ctx context.Context, owner, repository string, number int, assignees []string) error

	// GetAuthenticatedUser returns the login of the authenticated user.
	GetAuthenticatedUser(ctx context.Context) (string, error)

	// CreateRelease creates a release with a new tag.
	CreateRelease(ctx context.Context, req CreateReleaseRequest) (Release, error)

	// GetLatestTag returns the most recent semver tag for a repository,
	// or empty string if no tags exist.
	GetLatestTag(ctx context.Context, owner, repository string) (string, error)
}
