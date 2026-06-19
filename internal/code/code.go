package code

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Get-style methods when the requested
// resource doesn't exist on the host (e.g. GetReleaseByTag for a tag
// that has no release). Callers should branch on errors.Is rather
// than treating it as a fatal error.
var ErrNotFound = errors.New("code: not found")

// PullRequest represents a pull request on a code hosting platform.
type PullRequest struct {
	Number  int
	Title   string
	Body    string // Description text.
	URL     string
	State   string // "open" | "closed" | "merged"
	BaseRef string // target branch the PR merges into (e.g., "main")
	HeadSHA string // commit SHA at the PR's head — used for fetching checks

	// Review is the aggregate review decision derived from per-user
	// review states. Populated only for open PRs (closed / merged
	// PRs leave it empty since reviews don't matter once settled).
	// Values: "approved" | "changes_requested" | "awaiting" | ""
	// (no reviews requested or completed).
	Review string

	// MergeableState is GitHub's calculated mergeable state, encoding
	// the union of branch divergence, conflicts, required checks,
	// and required reviews. Populated only for open PRs. Don't
	// re-derive in client code — surface this directly. Values:
	// "clean" | "dirty" | "unstable" | "blocked" | "behind" | "draft"
	// | "unknown".
	MergeableState string

	// RequestedReviewers lists user logins currently pending review
	// (haven't acted yet, OR were re-requested after a review).
	// Reviewers who already submitted a review move out of this list,
	// so a stale entry here means "still waiting on them."
	RequestedReviewers []string
	// RequestedTeams lists team slugs currently pending review.
	// Same semantics as RequestedReviewers.
	RequestedTeams []string
	// Assignees lists user logins assigned to the PR.
	Assignees []string

	// AuthorLogin is the login of the user who opened the PR.
	// Populated by listing-style queries (PRsInRange) so callers can
	// attribute extra contributions in release ranges. May be empty
	// for endpoints that don't return author info.
	AuthorLogin string

	// MergeCommitSHA is the commit SHA that landed on the base
	// branch when the PR merged. Populated by listing-style queries
	// so callers can correlate PRs to git-log entries. Empty for
	// open or never-merged PRs.
	MergeCommitSHA string
}

// CheckRollup is the aggregate CI state for a commit's check runs.
// Folds GitHub's multi-suite, multi-check-run model into a single
// status-row summary suitable for at-a-glance display.
type CheckRollup struct {
	// State is the aggregate state derived from the counts:
	//   - "passing" — all checks succeeded (success / neutral / skipped)
	//   - "failing" — at least one check failed (failure / timed_out /
	//     cancelled / action_required)
	//   - "running" — at least one check still in_progress, none failing
	//   - "none"    — no checks defined for this commit
	State   string
	Passing int
	Failing int
	Running int
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

// EditPRRequest holds the fields updated on an existing pull request.
// Title, Body, and Base are overwritten with the given values.
type EditPRRequest struct {
	Owner      string // Repository owner (org or user)
	Repository string // Repository name
	Number     int    // PR number to edit
	Title      string
	Body       string
	Base       string // Target branch to retarget onto
}

// Release represents a release/tag on a code hosting platform.
type Release struct {
	Tag         string // e.g., "v1.2.3"
	URL         string
	Body        string // Release notes body — host-generated when CreateReleaseRequest.GenerateNotes is set.
	AuthorLogin string // Login of the user who cut the release (when known).
}

// CreateReleaseRequest holds the fields needed to create a release.
type CreateReleaseRequest struct {
	Owner      string
	Repository string
	Tag        string // e.g., "v1.2.3"
	Target     string // Branch or commit SHA to tag
	Name       string // Release title
	Body       string // Release notes

	// GenerateNotes asks the host to auto-generate release notes — a
	// changelog from the merged PRs/commits since the previous tag —
	// into the release body. When Body is also set, the host appends the
	// generated notes to it.
	GenerateNotes bool

	// PreviousTag pins the baseline for auto-generated notes. When
	// empty, the host picks its own previous tag (GitHub uses the
	// "latest" release, which can be wrong when newer tags exist
	// that aren't marked latest — e.g. legacy-api v4.19.142 picked
	// v4.19.130 because v4.19.141 wasn't the marked-latest release,
	// producing nonsense ranges and empty changelogs). Only honored
	// when GenerateNotes is also true.
	PreviousTag string
}

// Host defines code hosting operations needed by bosun.
type Host interface {
	// CreatePR creates a pull request. If a PR already exists for the
	// head branch, it returns the existing PR (idempotent).
	CreatePR(ctx context.Context, req CreatePRRequest) (PullRequest, error)

	// GetPRForBranch returns the PR for a given head branch. Returns a
	// PullRequest with Number==0 if none exists.
	GetPRForBranch(ctx context.Context, owner, repository, branch string) (PullRequest, error)

	// EditPR overwrites the title, body, and base branch of an existing PR.
	EditPR(ctx context.Context, req EditPRRequest) error

	// RequestReviewers requests reviews from the given users and/or teams on a PR.
	RequestReviewers(ctx context.Context, owner, repository string, number int, reviewers, teamReviewers []string) error

	// RemoveReviewers withdraws review requests from the given users and/or
	// teams on a PR. Only pending (requested) reviews can be withdrawn; a
	// review already submitted is unaffected.
	RemoveReviewers(ctx context.Context, owner, repository string, number int, reviewers, teamReviewers []string) error

	// AddAssignees adds assignees to a pull request.
	AddAssignees(ctx context.Context, owner, repository string, number int, assignees []string) error

	// RemoveAssignees removes assignees from a pull request.
	RemoveAssignees(ctx context.Context, owner, repository string, number int, assignees []string) error

	// GetAuthenticatedUser returns the login of the authenticated user.
	GetAuthenticatedUser(ctx context.Context) (string, error)

	// ListBranches returns branch names for a repository.
	ListBranches(ctx context.Context, owner, repository string) ([]string, error)

	// ListCollaborators returns usernames who can review or be assigned to PRs.
	ListCollaborators(ctx context.Context, owner, repository string) ([]string, error)

	// ListTeams returns team slugs for an organization.
	ListTeams(ctx context.Context, owner string) ([]string, error)

	// CreateRelease creates a release with a new tag.
	CreateRelease(ctx context.Context, req CreateReleaseRequest) (Release, error)

	// GetLatestTag returns the most recent semver tag for a repository,
	// or empty string if no tags exist.
	GetLatestTag(ctx context.Context, owner, repository string) (string, error)

	// GetReleaseByTag fetches the release published at the given tag.
	// Returns ErrNotFound when no release exists for that tag (the
	// tag may exist as a plain git tag without a corresponding
	// release record).
	GetReleaseByTag(ctx context.Context, owner, repository, tag string) (Release, error)

	// PRsInRange lists PRs whose merge commits land in (baseRef,
	// headRef] on the host. Used by prerelease to enumerate the
	// "extras" — PRs that will be swept into a release range
	// beyond the workspace's own contributions. Each returned
	// PullRequest has Number, Title, AuthorLogin, MergeCommitSHA,
	// and URL populated. excludeNumbers (typically the workspace's
	// own PR numbers) are filtered out of the result.
	PRsInRange(ctx context.Context, owner, repository, baseRef, headRef string, excludeNumbers []int) ([]PullRequest, error)

	// GetChecks returns the aggregate check status for a commit ref
	// (e.g., a PR head SHA or a branch HEAD). Returns CheckRollup
	// with State="none" if no checks exist for the ref.
	GetChecks(ctx context.Context, owner, repository, ref string) (CheckRollup, error)
}
