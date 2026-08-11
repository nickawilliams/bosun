package fakes

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/nickawilliams/bosun/internal/code"
)

// releaseTag matches release-shaped tags (v1.2.3) so GetLatestTag can
// ignore non-release tags, mirroring the GitHub adapter's behavior.
var releaseTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// Host is an in-memory code.Host. Seed it with PRs and releases via
// SeedPR / SeedRelease before invoking commands; inspect Releases()
// and Calls() after to assert on behavior. Safe for concurrent use.
//
// Tag realism: GitHub creates the release tag server-side when a
// release is cut. LinkRepo wires an owner/name pair to a real git
// repository (the workspace's bare remote) so CreateRelease creates an
// actual git tag there and GetLatestTag reads real tags back — the
// command's local git logic (FetchTags, TagsContaining, IsMergedInto)
// then sees exactly what it would against GitHub. Unlinked repos fall
// back to purely in-memory state.
type Host struct {
	mu sync.Mutex

	// repoDirs maps "owner/name" to a git repository directory
	// (typically the workspace's bare remote) for real tag operations.
	repoDirs map[string]string
	// prs are keyed by "owner/name@branch".
	prs map[string]code.PullRequest
	// releasesByTag are keyed by "owner/name@tag" — both seeded and
	// created releases, the lookup surface for GetReleaseByTag.
	releasesByTag map[string]code.Release
	// created lists releases created via CreateRelease, in order.
	created []code.Release
	// createRequests records every CreateRelease request verbatim.
	createRequests []code.CreateReleaseRequest
	// prsInRange are keyed by "owner/name" — returned by PRsInRange
	// minus the excluded numbers.
	prsInRange map[string][]code.PullRequest
	// latestTag is the fallback for unlinked repos, keyed "owner/name".
	latestTag map[string]string

	// CreateReleaseErr, GetLatestTagErr, GetPRErr, MergePRErr override
	// default behavior to force error paths. nil means success.
	CreateReleaseErr error
	GetLatestTagErr  error
	GetPRErr         error
	MergePRErr       error

	// calls records the method names invoked, in order.
	calls []string
}

// NewHost constructs an empty Host.
func NewHost() *Host {
	return &Host{
		repoDirs:      map[string]string{},
		prs:           map[string]code.PullRequest{},
		releasesByTag: map[string]code.Release{},
		prsInRange:    map[string][]code.PullRequest{},
		latestTag:     map[string]string{},
	}
}

func repoKey(owner, name string) string { return owner + "/" + name }

// LinkRepo wires owner/name to a git repository directory so
// CreateRelease creates real tags there and GetLatestTag derives the
// latest release tag from the repository's actual tags.
func (h *Host) LinkRepo(owner, name, dir string) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.repoDirs[repoKey(owner, name)] = dir
	return h
}

// SeedPR registers the PR returned by GetPRForBranch for a head branch.
func (h *Host) SeedPR(owner, name, branch string, pr code.PullRequest) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prs[repoKey(owner, name)+"@"+branch] = pr
	return h
}

// SeedRelease registers an existing release so GetReleaseByTag finds it.
func (h *Host) SeedRelease(owner, name string, rel code.Release) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.releasesByTag[repoKey(owner, name)+"@"+rel.Tag] = rel
	return h
}

// SeedPRsInRange registers the PR list PRsInRange returns for a repo
// (before exclude-number filtering).
func (h *Host) SeedPRsInRange(owner, name string, prs []code.PullRequest) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prsInRange[repoKey(owner, name)] = prs
	return h
}

// SeedLatestTag sets GetLatestTag's answer for an UNLINKED repo.
// Linked repos derive the latest tag from real git tags instead.
func (h *Host) SeedLatestTag(owner, name, tag string) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.latestTag[repoKey(owner, name)] = tag
	return h
}

// Releases returns a snapshot of releases created via CreateRelease,
// in creation order. Seeded releases are not included.
func (h *Host) Releases() []code.Release {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]code.Release, len(h.created))
	copy(out, h.created)
	return out
}

// CreateRequests returns a snapshot of every CreateRelease request.
func (h *Host) CreateRequests() []code.CreateReleaseRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]code.CreateReleaseRequest, len(h.createRequests))
	copy(out, h.createRequests)
	return out
}

// Calls returns a snapshot of method calls in invocation order.
func (h *Host) Calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.calls))
	copy(out, h.calls)
	return out
}

func (h *Host) recordCall(name string) {
	h.calls = append(h.calls, name)
}

// git runs a git command in dir and returns trimmed stdout.
func (h *Host) git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in %s: %w\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// --- code.Host implementation ---

func (h *Host) CreateRelease(_ context.Context, req code.CreateReleaseRequest) (code.Release, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("CreateRelease")
	h.createRequests = append(h.createRequests, req)
	if h.CreateReleaseErr != nil {
		return code.Release{}, h.CreateReleaseErr
	}

	key := repoKey(req.Owner, req.Repository)

	// GitHub-side semantics: cutting a release creates the tag at the
	// target ref. Mirror that on the linked repository so the local git
	// logic downstream (fetches, ancestry checks) sees the tag.
	if dir, ok := h.repoDirs[key]; ok {
		if _, err := h.git(dir, "tag", req.Tag, req.Target); err != nil {
			return code.Release{}, err
		}
	}

	body := req.Body
	if req.GenerateNotes {
		prev := req.PreviousTag
		if prev == "" {
			prev = "(auto)"
		}
		note := fmt.Sprintf("Changes in %s (since %s)", req.Tag, prev)
		if body != "" {
			body += "\n\n" + note
		} else {
			body = note
		}
	}

	rel := code.Release{
		Tag:         req.Tag,
		URL:         fmt.Sprintf("https://github.test/%s/%s/releases/tag/%s", req.Owner, req.Repository, req.Tag),
		Body:        body,
		AuthorLogin: "testuser",
	}
	h.created = append(h.created, rel)
	h.releasesByTag[key+"@"+rel.Tag] = rel
	h.latestTag[key] = rel.Tag
	return rel, nil
}

func (h *Host) GetLatestTag(_ context.Context, owner, repository string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetLatestTag")
	if h.GetLatestTagErr != nil {
		return "", h.GetLatestTagErr
	}
	key := repoKey(owner, repository)
	dir, ok := h.repoDirs[key]
	if !ok {
		return h.latestTag[key], nil
	}
	out, err := h.git(dir, "tag", "--list")
	if err != nil {
		return "", err
	}
	latest := ""
	for tag := range strings.SplitSeq(out, "\n") {
		if !releaseTag.MatchString(tag) {
			continue
		}
		if latest == "" || semverLess(latest, tag) {
			latest = tag
		}
	}
	return latest, nil
}

func (h *Host) GetReleaseByTag(_ context.Context, owner, repository, tag string) (code.Release, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetReleaseByTag")
	rel, ok := h.releasesByTag[repoKey(owner, repository)+"@"+tag]
	if !ok {
		return code.Release{}, code.ErrNotFound
	}
	return rel, nil
}

func (h *Host) GetPRForBranch(_ context.Context, owner, repository, branch string) (code.PullRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetPRForBranch")
	if h.GetPRErr != nil {
		return code.PullRequest{}, h.GetPRErr
	}
	return h.prs[repoKey(owner, repository)+"@"+branch], nil
}

func (h *Host) PRsInRange(_ context.Context, owner, repository, _, _ string, excludeNumbers []int) ([]code.PullRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("PRsInRange")
	exclude := make(map[int]bool, len(excludeNumbers))
	for _, n := range excludeNumbers {
		exclude[n] = true
	}
	var out []code.PullRequest
	for _, pr := range h.prsInRange[repoKey(owner, repository)] {
		if !exclude[pr.Number] {
			out = append(out, pr)
		}
	}
	return out, nil
}

func (h *Host) MergePR(_ context.Context, owner, repository string, number int, _ string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("MergePR")
	if h.MergePRErr != nil {
		return "", h.MergePRErr
	}
	// Flip the matching seeded PR to merged so post-merge lookups see
	// the settled state.
	for k, pr := range h.prs {
		if strings.HasPrefix(k, repoKey(owner, repository)+"@") && pr.Number == number {
			pr.State = "merged"
			h.prs[k] = pr
			return pr.MergeCommitSHA, nil
		}
	}
	return "", fmt.Errorf("no PR #%d seeded for %s/%s", number, owner, repository)
}

func (h *Host) CreatePR(_ context.Context, req code.CreatePRRequest) (code.PullRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("CreatePR")
	key := repoKey(req.Owner, req.Repository) + "@" + req.Head
	if existing, ok := h.prs[key]; ok && existing.Number > 0 {
		return existing, nil // idempotent, like the real host
	}
	pr := code.PullRequest{
		Number:  len(h.prs) + 1,
		Title:   req.Title,
		Body:    req.Body,
		State:   "open",
		BaseRef: req.Base,
		URL:     fmt.Sprintf("https://github.test/%s/%s/pull/%d", req.Owner, req.Repository, len(h.prs)+1),
	}
	h.prs[key] = pr
	return pr, nil
}

func (h *Host) EditPR(_ context.Context, _ code.EditPRRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("EditPR")
	return nil
}

func (h *Host) RequestReviewers(_ context.Context, _, _ string, _ int, _, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("RequestReviewers")
	return nil
}

func (h *Host) RemoveReviewers(_ context.Context, _, _ string, _ int, _, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("RemoveReviewers")
	return nil
}

func (h *Host) AddAssignees(_ context.Context, _, _ string, _ int, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("AddAssignees")
	return nil
}

func (h *Host) RemoveAssignees(_ context.Context, _, _ string, _ int, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("RemoveAssignees")
	return nil
}

func (h *Host) GetAuthenticatedUser(_ context.Context) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetAuthenticatedUser")
	return "testuser", nil
}

func (h *Host) ListBranches(_ context.Context, _, _ string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListBranches")
	return nil, nil
}

func (h *Host) ListCollaborators(_ context.Context, _, _ string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListCollaborators")
	return nil, nil
}

func (h *Host) ListTeams(_ context.Context, _ string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListTeams")
	return nil, nil
}

func (h *Host) GetChecks(_ context.Context, _, _, _ string) (code.CheckRollup, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetChecks")
	return code.CheckRollup{State: "none"}, nil
}

func (h *Host) GetLatestDeployment(_ context.Context, _, _, _ string) (code.Deployment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetLatestDeployment")
	return code.Deployment{}, code.ErrNotFound
}

// semverLess reports a < b for release-shaped tags.
func semverLess(a, b string) bool {
	pa, pb := semverParts(a), semverParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func semverParts(tag string) [3]int {
	var out [3]int
	m := releaseTag.FindStringSubmatch(tag)
	if m == nil {
		return out
	}
	for i := range 3 {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

// Verify Host satisfies code.Host at compile time.
var _ code.Host = (*Host)(nil)
