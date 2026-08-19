package fakes

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
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
	// createPRRequests / editPRRequests record every CreatePR / EditPR
	// request verbatim, so tests can assert the per-repository PR
	// metadata (base, title, body) each repo was given.
	createPRRequests []code.CreatePRRequest
	editPRRequests   []code.EditPRRequest
	// reviewerRequests / teamRequests / assigneeRequests record who was
	// requested or assigned, keyed "owner/name#number". Users and teams
	// are kept apart because RequestReviewers takes them as separate
	// arguments and the host treats them differently — folding them into
	// one list would let a test asserting on a team pass when the value
	// was sent as an individual reviewer.
	reviewerRequests map[string][]string
	teamRequests     map[string][]string
	assigneeRequests map[string][]string
	// prsInRange are keyed by "owner/name" — returned by PRsInRange
	// minus the excluded numbers.
	prsInRange map[string][]code.PullRequest
	// latestTag is the fallback for unlinked repos, keyed "owner/name".
	latestTag map[string]string
	// checksRefs records the "owner/name@ref" of every GetChecks call,
	// in order. The caller picks the ref (status resolves it to the
	// PR's head SHA when a PR exists, the branch otherwise), so it is
	// the only place that choice is observable.
	checksRefs []string
	// defaultBranches are keyed "owner/name"; unseeded repos answer
	// defaultBranchFallback.
	defaultBranches map[string]string
	// collaborators and teams are keyed "owner/name" and "owner"
	// respectively; unseeded lookups return nil.
	collaborators map[string][]string
	teams         map[string][]string
	// deployments are keyed "owner/name@environment" — the latest
	// deployment of that environment. An unseeded environment answers
	// code.ErrNotFound ("never deployed"), which is the distinction
	// release's classifier draws against a lookup failure.
	deployments map[string]code.Deployment

	// CreateReleaseErr, GetLatestTagErr, GetPRErr, CreatePRErr,
	// EditPRErr, RequestReviewersErr, AddAssigneesErr, MergePRErr,
	// GetDefaultBranchErr, ListCollaboratorsErr, ListTeamsErr,
	// GetLatestDeploymentErr override default behavior to force error
	// paths. nil means success.
	//
	// EditPRErr / RequestReviewersErr / AddAssigneesErr exist for the
	// best-effort writes that follow a PR create or update: the caller
	// reports them and keeps going, which is only observable if the
	// write can be made to fail.
	CreateReleaseErr       error
	GetLatestTagErr        error
	GetPRErr               error
	CreatePRErr            error
	EditPRErr              error
	RequestReviewersErr    error
	AddAssigneesErr        error
	MergePRErr             error
	GetDefaultBranchErr    error
	ListCollaboratorsErr   error
	ListTeamsErr           error
	GetLatestDeploymentErr error

	// AuthErr forces GetAuthenticatedUser to fail — the seam for
	// "host constructed, but the token is rejected".
	AuthErr error

	// NewErr makes the harness's CodeHost factory return this error
	// instead of the fake, simulating a host that failed to construct
	// (no token configured, bad credentials). See the same knob on
	// fakes.Tracker for why it lives on the fake.
	NewErr error

	// calls records the method names invoked, in order.
	calls []string
}

// defaultBranchFallback is GetDefaultBranch's answer for a repo no test
// seeded — the overwhelmingly common real-world default, so tests that
// don't care about base resolution need no setup.
const defaultBranchFallback = "main"

// NewHost constructs an empty Host.
func NewHost() *Host {
	return &Host{
		repoDirs:        map[string]string{},
		prs:             map[string]code.PullRequest{},
		releasesByTag:   map[string]code.Release{},
		prsInRange:      map[string][]code.PullRequest{},
		latestTag:       map[string]string{},
		defaultBranches: map[string]string{},
		collaborators:   map[string][]string{},
		teams:           map[string][]string{},
		deployments:     map[string]code.Deployment{},

		reviewerRequests: map[string][]string{},
		teamRequests:     map[string][]string{},
		assigneeRequests: map[string][]string{},
	}
}

func repoKey(owner, name string) string { return owner + "/" + name }

func prKey(owner, name string, number int) string {
	return repoKey(owner, name) + "#" + strconv.Itoa(number)
}

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

// SeedDefaultBranch sets the branch GetDefaultBranch returns for a
// repo. Unseeded repos answer defaultBranchFallback.
func (h *Host) SeedDefaultBranch(owner, name, branch string) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.defaultBranches[repoKey(owner, name)] = branch
	return h
}

// SeedDeployment sets the deployment GetLatestDeployment returns for
// an environment — the "what's live in production right now" half of
// release's deploy classification. Environments no test seeds answer
// code.ErrNotFound (never deployed).
//
// State is honored rather than decorative: the production contract is
// the most recent *successful* deployment, with failed or inactive
// ones skipped so they aren't read as what's live. A seed with any
// State other than "" or "success" therefore reads back as
// ErrNotFound, the same as the real adapter — seeding a failed deploy
// and asserting it counts as live would be asserting on a state the
// host cannot produce.
func (h *Host) SeedDeployment(owner, name, environment string, dep code.Deployment) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	if dep.Environment == "" {
		dep.Environment = environment
	}
	h.deployments[repoKey(owner, name)+"@"+environment] = dep
	return h
}

// SeedCollaborators sets the logins ListCollaborators returns for a repo.
func (h *Host) SeedCollaborators(owner, name string, logins ...string) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.collaborators[repoKey(owner, name)] = logins
	return h
}

// SeedTeams sets the slugs ListTeams returns for an org.
func (h *Host) SeedTeams(owner string, slugs ...string) *Host {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.teams[owner] = slugs
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

// ChecksRefs returns a snapshot of the "owner/name@ref" arguments
// passed to GetChecks, in call order. Use it to assert which ref a
// command asked for — the fake's rollup is fixed, so the ref is the
// interesting half of the call.
func (h *Host) ChecksRefs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.checksRefs))
	copy(out, h.checksRefs)
	return out
}

// CreatePRRequests returns a snapshot of every CreatePR request, in
// call order.
func (h *Host) CreatePRRequests() []code.CreatePRRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]code.CreatePRRequest, len(h.createPRRequests))
	copy(out, h.createPRRequests)
	return out
}

// EditPRRequests returns a snapshot of every EditPR request, in call order.
func (h *Host) EditPRRequests() []code.EditPRRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]code.EditPRRequest, len(h.editPRRequests))
	copy(out, h.editPRRequests)
	return out
}

// ReviewersRequested returns the individual users requested as
// reviewers across every PR in a repo: grouped by PR (PRs in lexical
// key order, NOT call order), requests within a PR in call order. Keyed
// by repo rather than PR number because callers assert on what a
// REPOSITORY was given — the number a freshly created PR happens to get
// is an artifact of seeding order.
//
// Team reviewers are reported separately by TeamsRequested.
func (h *Host) ReviewersRequested(owner, name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requestsForRepo(h.reviewerRequests, owner, name)
}

// TeamsRequested returns the teams requested as reviewers across every
// PR in a repo, with the same grouping and ordering as
// ReviewersRequested.
func (h *Host) TeamsRequested(owner, name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requestsForRepo(h.teamRequests, owner, name)
}

// AssigneesAdded returns the users assigned across every PR in a repo,
// with the same grouping and ordering as ReviewersRequested.
func (h *Host) AssigneesAdded(owner, name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requestsForRepo(h.assigneeRequests, owner, name)
}

// requestsForRepo flattens a PR-keyed request map down to one repo.
// Callers hold h.mu.
func (h *Host) requestsForRepo(m map[string][]string, owner, name string) []string {
	prefix := repoKey(owner, name) + "#"
	var out []string
	keys := make([]string, 0, len(m))
	for k := range m {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, m[k]...)
	}
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
	h.createPRRequests = append(h.createPRRequests, req)
	if h.CreatePRErr != nil {
		return code.PullRequest{}, h.CreatePRErr
	}
	key := repoKey(req.Owner, req.Repository) + "@" + req.Head
	if existing, ok := h.prs[key]; ok && existing.Number > 0 {
		return existing, nil // idempotent, like the real host
	}
	// Fuller than the GitHub adapter, which returns only number/URL/
	// state from its create call — BaseRef and the echoed title/body are
	// here so a follow-up GetPRForBranch in the same test sees a
	// realistic PR. Assert on CreatePRRequests for what was *asked for*;
	// asserting these fields on the returned PR tests the fake.
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

func (h *Host) EditPR(_ context.Context, req code.EditPRRequest) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("EditPR")
	h.editPRRequests = append(h.editPRRequests, req)
	if h.EditPRErr != nil {
		return h.EditPRErr
	}
	// Mirror the host: the edit lands on the PR, so a later lookup (or
	// a second run in the same test) sees the updated content.
	for k, pr := range h.prs {
		if strings.HasPrefix(k, repoKey(req.Owner, req.Repository)+"@") && pr.Number == req.Number {
			pr.Title, pr.Body, pr.BaseRef = req.Title, req.Body, req.Base
			h.prs[k] = pr
			break
		}
	}
	return nil
}

func (h *Host) RequestReviewers(_ context.Context, owner, repository string, number int, reviewers, teamReviewers []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("RequestReviewers")
	if h.RequestReviewersErr != nil {
		return h.RequestReviewersErr
	}
	key := prKey(owner, repository, number)
	h.reviewerRequests[key] = append(h.reviewerRequests[key], reviewers...)
	h.teamRequests[key] = append(h.teamRequests[key], teamReviewers...)
	// Mirror the host: a requested reviewer shows up as pending on the
	// PR, so a second run sees them already satisfied rather than
	// re-requesting (and resetting) them. Appends unconditionally where
	// GitHub would de-duplicate — strictly noisier than the real thing,
	// so a test that passes here would pass against the host too.
	h.recordOnPR(owner, repository, number, func(pr *code.PullRequest) {
		pr.RequestedReviewers = append(pr.RequestedReviewers, reviewers...)
		pr.RequestedTeams = append(pr.RequestedTeams, teamReviewers...)
	})
	return nil
}

// recordOnPR applies mutate to the seeded PR with the given number in
// owner/repository, if one exists. Callers hold h.mu.
func (h *Host) recordOnPR(owner, repository string, number int, mutate func(*code.PullRequest)) {
	for k, pr := range h.prs {
		if strings.HasPrefix(k, repoKey(owner, repository)+"@") && pr.Number == number {
			mutate(&pr)
			h.prs[k] = pr
			return
		}
	}
}

func (h *Host) RemoveReviewers(_ context.Context, _, _ string, _ int, _, _ []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("RemoveReviewers")
	return nil
}

func (h *Host) AddAssignees(_ context.Context, owner, repository string, number int, assignees []string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("AddAssignees")
	if h.AddAssigneesErr != nil {
		return h.AddAssigneesErr
	}
	key := prKey(owner, repository, number)
	h.assigneeRequests[key] = append(h.assigneeRequests[key], assignees...)
	h.recordOnPR(owner, repository, number, func(pr *code.PullRequest) {
		pr.Assignees = append(pr.Assignees, assignees...)
	})
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
	if h.AuthErr != nil {
		return "", h.AuthErr
	}
	return "testuser", nil
}

func (h *Host) ListBranches(_ context.Context, _, _ string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListBranches")
	return nil, nil
}

func (h *Host) GetDefaultBranch(_ context.Context, owner, repository string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetDefaultBranch")
	if h.GetDefaultBranchErr != nil {
		return "", h.GetDefaultBranchErr
	}
	if b, ok := h.defaultBranches[repoKey(owner, repository)]; ok {
		return b, nil
	}
	return defaultBranchFallback, nil
}

func (h *Host) ListCollaborators(_ context.Context, owner, repository string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListCollaborators")
	if h.ListCollaboratorsErr != nil {
		return nil, h.ListCollaboratorsErr
	}
	return h.collaborators[repoKey(owner, repository)], nil
}

func (h *Host) ListTeams(_ context.Context, owner string) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("ListTeams")
	if h.ListTeamsErr != nil {
		return nil, h.ListTeamsErr
	}
	return h.teams[owner], nil
}

func (h *Host) GetChecks(_ context.Context, owner, repository, ref string) (code.CheckRollup, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetChecks")
	h.checksRefs = append(h.checksRefs, repoKey(owner, repository)+"@"+ref)
	return code.CheckRollup{State: "none"}, nil
}

func (h *Host) GetLatestDeployment(_ context.Context, owner, repository, environment string) (code.Deployment, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordCall("GetLatestDeployment")
	if h.GetLatestDeploymentErr != nil {
		return code.Deployment{}, h.GetLatestDeploymentErr
	}
	dep, ok := h.deployments[repoKey(owner, repository)+"@"+environment]
	if !ok {
		return code.Deployment{}, code.ErrNotFound
	}
	// "Most recent SUCCESSFUL deployment" — a failed or inactive one is
	// skipped by the real adapter rather than reported as live. An
	// unset State is taken as success so scenarios that don't care can
	// leave it off.
	if dep.State != "" && dep.State != "success" {
		return code.Deployment{}, code.ErrNotFound
	}
	return dep, nil
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

// --- Remote and web-URL surface ---
//
// The harness wires each repo's origin to a GitHub-shaped
// git@github.com:acme/<name>.git (see testharness.Workspace.AddRepo), so
// the fake reads remotes with the shared implementation and mints
// github.com web URLs. Commands that link to a repository therefore
// produce exactly the strings a real GitHub host would, which is what
// makes output assertions on those links meaningful.
//
// None of these record a call. Tests read Calls() to police what a
// command costs — network round-trips and mutations — and none of this
// is either: ParseRemote shells out to local git, and the URL builders
// are pure string formatting. Logging them would force every
// call-allowlist in the suite to name methods that can't misbehave.

func (h *Host) ParseRemote(ctx context.Context, repositoryPath string) (code.RepositoryIdentity, error) {
	return code.ParseRemote(ctx, repositoryPath)
}

func (h *Host) RepositoryURL(repo code.RepositoryIdentity) string {
	return fmt.Sprintf("https://github.com/%s/%s", repo.Owner, repo.Name)
}

func (h *Host) BranchURL(repo code.RepositoryIdentity, branch string) string {
	return fmt.Sprintf("https://github.com/%s/%s/tree/%s", repo.Owner, repo.Name, branch)
}

func (h *Host) ChecksURL(repo code.RepositoryIdentity, ref string) string {
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s/checks", repo.Owner, repo.Name, ref)
}

func (h *Host) AvatarURL(login string, size int) string {
	return fmt.Sprintf("https://github.com/%s.png?size=%d", login, size)
}

// Verify Host satisfies code.Host at compile time.
var _ code.Host = (*Host)(nil)
