package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nickawilliams/bosun/internal/code"
)

// Adapter implements code.Host using the GitHub REST API v3.
type Adapter struct {
	client  *http.Client
	baseURL string
	token   string
}

// New returns a new GitHub adapter.
func New(token string) *Adapter {
	return &Adapter{
		client:  http.DefaultClient,
		baseURL: "https://api.github.com",
		token:   token,
	}
}

// NewWithClient returns a GitHub adapter with a custom HTTP client and
// base URL (for testing).
func NewWithClient(client *http.Client, baseURL, token string) *Adapter {
	return &Adapter{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// ResolveToken tries to get a GitHub token from:
// 1. gh auth token (GitHub CLI)
// 2. GITHUB_TOKEN environment variable
// Returns empty string if neither works.
func ResolveToken() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, "gh", "auth", "token")
		out, err := cmd.Output()
		if err == nil {
			token := strings.TrimSpace(string(out))
			if token != "" {
				return token
			}
		}
	}

	return os.Getenv("GITHUB_TOKEN")
}

func (a *Adapter) CreatePR(ctx context.Context, req code.CreatePRRequest) (code.PullRequest, error) {
	// Check for existing PR first (idempotent).
	existing, err := a.GetPRForBranch(ctx, req.Owner, req.Repository, req.Head)
	if err != nil {
		return code.PullRequest{}, err
	}
	if existing.Number > 0 {
		return existing, nil
	}

	body := map[string]any{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
		"draft": req.Draft,
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls", req.Owner, req.Repository)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return code.PullRequest{}, fmt.Errorf("creating PR: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.PullRequest{}, fmt.Errorf("parsing PR response: %w", err)
	}

	return code.PullRequest{
		Number: result.Number,
		Title:  result.Title,
		Body:   result.Body,
		URL:    result.HTMLURL,
		State:  result.State,
	}, nil
}

func (a *Adapter) GetPRForBranch(ctx context.Context, owner, repository, branch string) (code.PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?head=%s&state=all",
		owner, repository, url.QueryEscape(owner+":"+branch))
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return code.PullRequest{}, fmt.Errorf("fetching PR for branch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var results []struct {
		Number         int     `json:"number"`
		Title          string  `json:"title"`
		Body           string  `json:"body"`
		HTMLURL        string  `json:"html_url"`
		State          string  `json:"state"`
		Draft          bool    `json:"draft"`
		MergedAt       *string `json:"merged_at"`
		MergeCommitSHA string  `json:"merge_commit_sha"`
		User           struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
		RequestedTeams []struct {
			Slug string `json:"slug"`
		} `json:"requested_teams"`
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return code.PullRequest{}, fmt.Errorf("parsing PR list response: %w", err)
	}

	if len(results) == 0 {
		return code.PullRequest{}, nil
	}

	// The query is state=all, so a branch reused after a closed/merged
	// PR returns multiple results. Prefer an open PR (the active one a
	// caller cares about — review modifies it, status shows it); fall
	// back to results[0] (most recent) for closed/merged-only display.
	raw := results[0]
	for _, r := range results {
		if r.State == "open" {
			raw = r
			break
		}
	}
	state := raw.State
	if raw.MergedAt != nil {
		state = "merged"
	} else if raw.State == "open" && raw.Draft {
		state = "draft"
	}

	pr := code.PullRequest{
		Number:         raw.Number,
		Title:          raw.Title,
		Body:           raw.Body,
		URL:            raw.HTMLURL,
		State:          state,
		BaseRef:        raw.Base.Ref,
		HeadSHA:        raw.Head.SHA,
		MergeCommitSHA: raw.MergeCommitSHA,
		AuthorLogin:    raw.User.Login,
	}
	for _, r := range raw.RequestedReviewers {
		pr.RequestedReviewers = append(pr.RequestedReviewers, r.Login)
	}
	for _, t := range raw.RequestedTeams {
		pr.RequestedTeams = append(pr.RequestedTeams, t.Slug)
	}
	for _, a := range raw.Assignees {
		pr.Assignees = append(pr.Assignees, a.Login)
	}

	// Only enrich open PRs (including drafts) — for terminal states
	// (merged / closed) the additional fields don't carry useful
	// signal and the extra fetches would be wasted. Enrichment is
	// best-effort: the PR demonstrably exists (the list call above
	// succeeded), and callers uniformly discard the whole value on a
	// non-nil error — so a transient blip on these detail calls would
	// misread as "no PR" (release gates hard-block on that). A failed
	// enrichment degrades to MergeableState "unknown" (GitHub's own
	// indeterminate value; canMergePR treats it as not mergeable) and
	// an empty review decision. A returned error therefore always
	// means the list itself failed — "couldn't determine", never
	// "doesn't exist".
	if state == "open" || state == "draft" {
		if mergeable, err := a.fetchMergeableState(ctx, owner, repository, raw.Number); err == nil {
			pr.MergeableState = mergeable
		} else {
			pr.MergeableState = "unknown"
		}
		if review, reviewedBy, err := a.fetchReviewDecision(ctx, owner, repository, raw.Number); err == nil {
			pr.Review = review
			pr.ReviewedBy = reviewedBy
		}
	}

	return pr, nil
}

// fetchMergeableState makes a single-PR detail call to read the
// `mergeable_state` field, which is not present in the list-PR
// response. Returned values match GitHub's documented set:
// "clean" | "dirty" | "unstable" | "blocked" | "behind" | "draft" |
// "unknown".
func (a *Adapter) fetchMergeableState(ctx context.Context, owner, repository string, number int) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repository, number)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var detail struct {
		MergeableState string `json:"mergeable_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return "", fmt.Errorf("parsing PR detail: %w", err)
	}
	return detail.MergeableState, nil
}

// fetchReviewDecision aggregates per-user reviews into a single
// review-decision value. Mirrors GitHub GraphQL's reviewDecision
// (which REST doesn't expose directly): any change request beats
// any approval; approval from anyone beats no decision; otherwise
// "awaiting" if reviews were requested but not yet completed; ""
// if no reviewers were involved at all.
//
// REST's reviews endpoint returns the full review history; we keep
// only the latest review per user (later reviews supersede earlier
// ones from the same person). Alongside the decision it returns the
// set of users who submitted ANY review (including comment-only) —
// GitHub drops them from requested_reviewers on submission, and
// reconciliation needs them to avoid re-requesting completed
// reviewers.
func (a *Adapter) fetchReviewDecision(ctx context.Context, owner, repository string, number int) (string, []string, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repository, number)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var reviews []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State       string `json:"state"`         // "APPROVED" | "CHANGES_REQUESTED" | "COMMENTED" | "DISMISSED" | "PENDING"
		SubmittedAt string `json:"submitted_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
		return "", nil, fmt.Errorf("parsing reviews: %w", err)
	}

	// Latest non-COMMENTED/PENDING review per user wins for the
	// decision; every submitter (PENDING excluded — those are
	// unsubmitted drafts) counts as having reviewed.
	latest := map[string]string{}
	seenReviewer := map[string]bool{}
	var reviewedBy []string
	for _, r := range reviews {
		if r.State != "PENDING" && r.User.Login != "" && !seenReviewer[r.User.Login] {
			seenReviewer[r.User.Login] = true
			reviewedBy = append(reviewedBy, r.User.Login)
		}
		// Skip COMMENTED and PENDING — they don't carry a decision.
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" && r.State != "DISMISSED" {
			continue
		}
		latest[r.User.Login] = r.State
	}

	approved, changesRequested := false, false
	for _, state := range latest {
		switch state {
		case "APPROVED":
			approved = true
		case "CHANGES_REQUESTED":
			changesRequested = true
		}
	}

	switch {
	case changesRequested:
		return "changes_requested", reviewedBy, nil
	case approved:
		return "approved", reviewedBy, nil
	}

	// No decisive reviews — check if any reviewers were requested
	// (which would map to "awaiting"). The reviews endpoint doesn't
	// surface this; the PR detail's requested_reviewers does.
	requested, err := a.hasRequestedReviewers(ctx, owner, repository, number)
	if err != nil {
		return "", reviewedBy, err
	}
	if requested {
		return "awaiting", reviewedBy, nil
	}
	return "", reviewedBy, nil
}

// hasRequestedReviewers returns true if the PR has any pending
// review requests (users or teams who haven't submitted a review).
func (a *Adapter) hasRequestedReviewers(ctx context.Context, owner, repository string, number int) (bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repository, number)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()

	var requested struct {
		Users []struct{} `json:"users"`
		Teams []struct{} `json:"teams"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&requested); err != nil {
		return false, fmt.Errorf("parsing requested reviewers: %w", err)
	}
	return len(requested.Users) > 0 || len(requested.Teams) > 0, nil
}

// GetChecks returns the aggregate check-runs status for a commit ref.
// Walks all check runs for the ref (multi-suite, multi-run model is
// flat-listed by this endpoint) and folds them into the 4-state
// CheckRollup.
func (a *Adapter) GetChecks(ctx context.Context, owner, repository, ref string) (code.CheckRollup, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", owner, repository, url.PathEscape(ref))
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return code.CheckRollup{}, fmt.Errorf("fetching check runs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		CheckRuns []struct {
			Status     string `json:"status"`     // "queued" | "in_progress" | "completed"
			Conclusion string `json:"conclusion"` // "success" | "failure" | "neutral" | "cancelled" | "skipped" | "timed_out" | "action_required" — set when status==completed
		} `json:"check_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.CheckRollup{}, fmt.Errorf("parsing check runs: %w", err)
	}

	rollup := code.CheckRollup{}
	for _, cr := range result.CheckRuns {
		if cr.Status != "completed" {
			rollup.Running++
			continue
		}
		switch cr.Conclusion {
		case "failure", "timed_out", "cancelled", "action_required":
			rollup.Failing++
		default: // success / neutral / skipped
			rollup.Passing++
		}
	}

	switch {
	case rollup.Passing+rollup.Failing+rollup.Running == 0:
		rollup.State = "none"
	case rollup.Failing > 0:
		rollup.State = "failing"
	case rollup.Running > 0:
		rollup.State = "running"
	default:
		rollup.State = "passing"
	}
	return rollup, nil
}

func (a *Adapter) CreateRelease(ctx context.Context, req code.CreateReleaseRequest) (code.Release, error) {
	body := map[string]any{
		"tag_name":         req.Tag,
		"target_commitish": req.Target,
		"name":             req.Name,
		"body":             req.Body,
	}
	if req.GenerateNotes {
		body["generate_release_notes"] = true
		if req.PreviousTag != "" {
			// Pin the baseline so GitHub doesn't fall back to its
			// /releases/latest pick, which can lag behind the actual
			// most-recent tag when interim releases weren't marked
			// "latest" (or weren't created as releases at all).
			body["previous_tag_name"] = req.PreviousTag
		}
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", req.Owner, req.Repository)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return code.Release{}, fmt.Errorf("creating release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.Release{}, fmt.Errorf("parsing release response: %w", err)
	}

	return code.Release{
		Tag: result.TagName,
		URL: result.HTMLURL,
		// Prettify GitHub's raw release-notes markdown so PR/issue/compare
		// URLs and `@username` mentions render as the display-friendly
		// link text GitHub uses on its own rendered release page. The
		// transformation happens entirely inside the adapter so the body
		// crosses the code.Host boundary as generic display-ready
		// Markdown — every layer above is provider-agnostic.
		Body: PrettifyReleaseNotes(result.Body),
	}, nil
}

// End-anchored so a prerelease tag (v1.2.3-rc.1) can't parse as its
// final release and win the "latest" pick over the real v1.2.3.
var semverTag = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// GetLatestTag returns the most recent semver tag for a repository.
// Resolution order:
//   1. GitHub's /releases/latest endpoint — respects the
//      marked-as-latest flag maintainers can set, and defaults to
//      the highest-semver release when unset. This is the
//      authoritative answer for "what's the current released version?"
//   2. Fall back to walking /tags and picking the highest-semver
//      tag, for repos that have tags but no releases (or where
//      /releases/latest 404s for an unrelated reason). Order-by-
//      semver, NOT the order the API returns — GitHub's /tags
//      endpoint sorts by creation time, which can put a stale,
//      manually-pushed tag ahead of the actual latest release.
//
// Returns empty string if neither path yields a semver-shaped tag.
func (a *Adapter) GetLatestTag(ctx context.Context, owner, repository string) (string, error) {
	// Marked-as-latest release is the authoritative signal.
	latestPath := fmt.Sprintf("/repos/%s/%s/releases/latest", owner, repository)
	resp, err := a.doRequest(ctx, http.MethodGet, latestPath, nil)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		var latest struct {
			TagName string `json:"tag_name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&latest); err == nil && latest.TagName != "" {
			return latest.TagName, nil
		}
	}
	// 404 (no releases) or decode failure falls through to the tag
	// walk — repos that tag but don't publish releases still have
	// a meaningful "latest" via semver ordering.

	// /tags returns 200 + [] for a repo with no tags, and 404 only
	// when the repo itself doesn't exist — so a 404 here is a real
	// error, not a "no tags yet" signal. Bubble it up so callers
	// (and the existing TestAPIError contract) see the failure.
	tagsPath := fmt.Sprintf("/repos/%s/%s/tags?per_page=100", owner, repository)
	tagsResp, err := a.doRequest(ctx, http.MethodGet, tagsPath, nil)
	if err != nil {
		return "", fmt.Errorf("fetching tags: %w", err)
	}
	defer func() { _ = tagsResp.Body.Close() }()

	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(tagsResp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("parsing tags response: %w", err)
	}

	best := ""
	bestParts := [3]int{-1, -1, -1}
	for _, t := range tags {
		m := semverTag.FindStringSubmatch(t.Name)
		if m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		patch, _ := strconv.Atoi(m[3])
		parts := [3]int{major, minor, patch}
		if best == "" || parts[0] > bestParts[0] ||
			(parts[0] == bestParts[0] && parts[1] > bestParts[1]) ||
			(parts[0] == bestParts[0] && parts[1] == bestParts[1] && parts[2] > bestParts[2]) {
			best = t.Name
			bestParts = parts
		}
	}
	return best, nil
}

// GetReleaseByTag fetches a release by its tag name. Returns
// code.ErrNotFound when GitHub responds 404 (the tag may exist as a
// plain git tag without a corresponding release record). The body is
// run through PrettifyReleaseNotes so consumers see the same
// display-ready Markdown that CreateRelease returns.
func (a *Adapter) GetReleaseByTag(ctx context.Context, owner, repository, tag string) (code.Release, error) {
	path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s", owner, repository, url.PathEscape(tag))
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		if isNotFound(err) {
			return code.Release{}, code.ErrNotFound
		}
		return code.Release{}, fmt.Errorf("fetching release for tag %s: %w", tag, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		Author  struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.Release{}, fmt.Errorf("parsing release response: %w", err)
	}

	return code.Release{
		Tag:         result.TagName,
		URL:         result.HTMLURL,
		Body:        PrettifyReleaseNotes(result.Body),
		AuthorLogin: result.Author.Login,
	}, nil
}

// GetLatestDeployment returns the most recent successful deployment for
// an environment. GitHub returns deployments newest-first; we walk the
// pages and, for each deployment, read its latest status, returning the
// first whose state is "success" — so a failed or in-progress latest
// deployment doesn't get mistaken for what's live. Returns
// code.ErrNotFound when the environment has no deployments or none
// succeeded in the walked window (a missing environment is a 200 with
// an empty list; a 404 means the repo itself wasn't found and surfaces
// as an error, not ErrNotFound — "repo unreachable" must never read as
// "never deployed").
func (a *Adapter) GetLatestDeployment(ctx context.Context, owner, repository, environment string) (code.Deployment, error) {
	// Cap the walk: each deployment costs a statuses request, and a
	// success normally sits in the first few entries. Environments
	// with more consecutive non-success deployments than this read as
	// ErrNotFound ("nothing live"), which matches the likeliest truth.
	const maxDeployments = 90

	path := fmt.Sprintf("/repos/%s/%s/deployments?environment=%s&per_page=30",
		owner, repository, url.QueryEscape(environment))
	seen := 0
	for path != "" && seen < maxDeployments {
		resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return code.Deployment{}, fmt.Errorf("listing deployments for %s: %w", environment, err)
		}

		var deployments []struct {
			ID        int64     `json:"id"`
			SHA       string    `json:"sha"`
			Ref       string    `json:"ref"`
			CreatedAt time.Time `json:"created_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&deployments); err != nil {
			_ = resp.Body.Close()
			return code.Deployment{}, fmt.Errorf("parsing deployments: %w", err)
		}
		next := nextPagePath(resp.Header.Get("Link"))
		_ = resp.Body.Close()

		for _, d := range deployments {
			seen++
			state, err := a.latestDeploymentState(ctx, owner, repository, d.ID)
			if err != nil {
				return code.Deployment{}, err
			}
			if state == "success" {
				return code.Deployment{
					Environment: environment,
					Ref:         d.Ref,
					SHA:         d.SHA,
					State:       state,
					CreatedAt:   d.CreatedAt,
				}, nil
			}
			if seen >= maxDeployments {
				break
			}
		}
		path = next
	}
	return code.Deployment{}, code.ErrNotFound
}

// latestDeploymentState returns the most recent status state for a
// deployment ("success", "failure", "in_progress", "inactive", ...), or
// "" when it has no statuses yet.
func (a *Adapter) latestDeploymentState(ctx context.Context, owner, repository string, deploymentID int64) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/deployments/%d/statuses?per_page=1", owner, repository, deploymentID)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("fetching deployment %d statuses: %w", deploymentID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var statuses []struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return "", fmt.Errorf("parsing deployment statuses: %w", err)
	}
	if len(statuses) == 0 {
		return "", nil
	}
	return statuses[0].State, nil
}

// MergePR merges a pull request via PUT /pulls/{n}/merge with the given
// merge_method, returning the resulting merge commit SHA. GitHub rejects
// the request (405 / 409) when the PR isn't mergeable — branch
// protection, conflicts, or failing required checks — surfaced as an error.
func (a *Adapter) MergePR(ctx context.Context, owner, repository string, number int, method string) (string, error) {
	body := map[string]any{}
	if method != "" {
		body["merge_method"] = method
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repository, number)
	resp, err := a.doRequest(ctx, http.MethodPut, path, body)
	if err != nil {
		return "", fmt.Errorf("merging pull request #%d: %w", number, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing merge response: %w", err)
	}
	if !result.Merged {
		// A 200 with merged=false is GitHub declining the merge —
		// returning success with an empty SHA would let a merge-offer
		// caller proceed as if the PR landed.
		return "", fmt.Errorf("merging pull request #%d: %s", number, result.Message)
	}
	return result.SHA, nil
}

// PRsInRange lists PRs whose commits fall in (baseRef, headRef] for
// the given repository. Implementation: call the compare endpoint
// (paginated — the default page holds only the first commits of the
// range, which silently under-reported large ranges), then call
// /commits/{sha}/pulls for each commit to find the associated PRs.
// Deduplicated by PR number. excludeNumbers (typically the
// workspace's own PR numbers) is filtered out of the result.
//
// API cost: 1 compare call per 100 commits + N per-commit lookups,
// where N is the number of commits in the range, capped at
// maxRangeCommits. For typical release ranges (a handful of
// squash-merged PRs) this stays well under quota.
func (a *Adapter) PRsInRange(ctx context.Context, owner, repository, baseRef, headRef string, excludeNumbers []int) ([]code.PullRequest, error) {
	// Bounds the per-commit lookup fan-out for pathological ranges;
	// matches the classic compare-view ceiling. A range this size has
	// bigger problems than a truncated extras list.
	const maxRangeCommits = 250

	excluded := make(map[int]bool, len(excludeNumbers))
	for _, n := range excludeNumbers {
		excluded[n] = true
	}

	var shas []string
	path := fmt.Sprintf("/repos/%s/%s/compare/%s...%s?per_page=100",
		owner, repository, url.PathEscape(baseRef), url.PathEscape(headRef))
	for path != "" && len(shas) < maxRangeCommits {
		resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("comparing %s...%s: %w", baseRef, headRef, err)
		}
		var compareResp struct {
			Commits []struct {
				SHA string `json:"sha"`
			} `json:"commits"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&compareResp); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("parsing compare response: %w", err)
		}
		next := nextPagePath(resp.Header.Get("Link"))
		_ = resp.Body.Close()

		for _, c := range compareResp.Commits {
			shas = append(shas, c.SHA)
			if len(shas) >= maxRangeCommits {
				break
			}
		}
		path = next
	}

	seen := make(map[int]bool)
	var out []code.PullRequest
	for _, sha := range shas {
		prs, err := a.prsForCommit(ctx, owner, repository, sha)
		if err != nil {
			// A 404 on a rebased/orphaned commit just means "no PR
			// associated" — skip it. Anything else (auth, rate limit,
			// network) would hit every remaining lookup too; swallowing
			// it returned an empty list with err == nil, which callers
			// read as "no extra PRs in this release".
			if isNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("resolving PRs for commit %.12s: %w", sha, err)
		}
		for _, pr := range prs {
			if seen[pr.Number] || excluded[pr.Number] {
				continue
			}
			seen[pr.Number] = true
			out = append(out, pr)
		}
	}
	return out, nil
}

// prsForCommit wraps GET /repos/{o}/{r}/commits/{sha}/pulls. Returns
// the (typically one) PR associated with the commit. Used by
// PRsInRange to walk a commit list and attribute each commit to its
// originating PR.
func (a *Adapter) prsForCommit(ctx context.Context, owner, repository, sha string) ([]code.PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/pulls", owner, repository, sha)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var raw []struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		HTMLURL        string `json:"html_url"`
		State          string `json:"state"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		User           struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing commit pulls response: %w", err)
	}

	out := make([]code.PullRequest, 0, len(raw))
	for _, r := range raw {
		out = append(out, code.PullRequest{
			Number:         r.Number,
			Title:          r.Title,
			URL:            r.HTMLURL,
			State:          r.State,
			BaseRef:        r.Base.Ref,
			HeadSHA:        r.Head.SHA,
			MergeCommitSHA: r.MergeCommitSHA,
			AuthorLogin:    r.User.Login,
		})
	}
	return out, nil
}

// isNotFound returns true when err carries the marker text our
// doRequest uses for 404 responses. Kept as a local helper since the
// adapter's error wrapping doesn't preserve a typed 404 sentinel —
// upgrading doRequest to surface status codes structurally is a
// broader cleanup; this string-check is the minimum-viable bridge.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "HTTP 404")
}

func (a *Adapter) ListBranches(ctx context.Context, owner, repository string) ([]string, error) {
	var names []string
	path := fmt.Sprintf("/repos/%s/%s/branches?per_page=100", owner, repository)

	for path != "" {
		resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("listing branches: %w", err)
		}

		var page []struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("parsing branches response: %w", err)
		}
		_ = resp.Body.Close()

		for _, r := range page {
			names = append(names, r.Name)
		}

		path = nextPagePath(resp.Header.Get("Link"))
	}

	return names, nil
}

func (a *Adapter) ListCollaborators(ctx context.Context, owner, repository string) ([]string, error) {
	var logins []string
	path := fmt.Sprintf("/repos/%s/%s/collaborators?per_page=100", owner, repository)

	for path != "" {
		resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("listing collaborators: %w", err)
		}

		var page []struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("parsing collaborators response: %w", err)
		}
		_ = resp.Body.Close()

		for _, r := range page {
			logins = append(logins, r.Login)
		}

		path = nextPagePath(resp.Header.Get("Link"))
	}

	return logins, nil
}

func (a *Adapter) ListTeams(ctx context.Context, owner string) ([]string, error) {
	var slugs []string
	path := fmt.Sprintf("/orgs/%s/teams?per_page=100", owner)

	for path != "" {
		resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, fmt.Errorf("listing teams: %w", err)
		}

		var page []struct {
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("parsing teams response: %w", err)
		}
		_ = resp.Body.Close()

		for _, r := range page {
			slugs = append(slugs, r.Slug)
		}

		path = nextPagePath(resp.Header.Get("Link"))
	}

	return slugs, nil
}

func (a *Adapter) EditPR(ctx context.Context, req code.EditPRRequest) error {
	body := map[string]any{
		"title": req.Title,
		"body":  req.Body,
		"base":  req.Base,
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", req.Owner, req.Repository, req.Number)
	resp, err := a.doRequest(ctx, http.MethodPatch, path, body)
	if err != nil {
		return fmt.Errorf("editing pull request: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) RequestReviewers(ctx context.Context, owner, repo string, number int, reviewers, teamReviewers []string) error {
	body := map[string]any{}
	if len(reviewers) > 0 {
		body["reviewers"] = reviewers
	}
	if len(teamReviewers) > 0 {
		body["team_reviewers"] = teamReviewers
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("requesting reviewers: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) RemoveReviewers(ctx context.Context, owner, repo string, number int, reviewers, teamReviewers []string) error {
	body := map[string]any{}
	if len(reviewers) > 0 {
		body["reviewers"] = reviewers
	}
	if len(teamReviewers) > 0 {
		body["team_reviewers"] = teamReviewers
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number)
	resp, err := a.doRequest(ctx, http.MethodDelete, path, body)
	if err != nil {
		return fmt.Errorf("removing reviewers: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) AddAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	body := map[string]any{"assignees": assignees}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", owner, repo, number)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("adding assignees: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) RemoveAssignees(ctx context.Context, owner, repo string, number int, assignees []string) error {
	body := map[string]any{"assignees": assignees}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", owner, repo, number)
	resp, err := a.doRequest(ctx, http.MethodDelete, path, body)
	if err != nil {
		return fmt.Errorf("removing assignees: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) GetAuthenticatedUser(ctx context.Context) (string, error) {
	resp, err := a.doRequest(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return "", fmt.Errorf("getting authenticated user: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing user response: %w", err)
	}
	return result.Login, nil
}

// doRequest executes an authenticated request against the GitHub API.
func (a *Adapter) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	url := a.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github API error (HTTP %d): %s", resp.StatusCode, respBody)
	}

	return resp, nil
}

// nextPagePath extracts the path (with query) for the next page from a
// GitHub Link header. Returns empty string if there is no next page.
// GitHub may use /repos/{owner}/{repo}/... or /repositories/{id}/... forms.
func nextPagePath(link string) string {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end < 0 || end <= start {
			continue
		}
		rawURL := part[start+1 : end]
		// Strip scheme + host to get path+query.
		// E.g., "https://api.github.com/repositories/123/branches?page=2"
		// → "/repositories/123/branches?page=2"
		if idx := strings.Index(rawURL, "://"); idx >= 0 {
			rest := rawURL[idx+3:] // skip "://"
			if slash := strings.Index(rest, "/"); slash >= 0 {
				return rest[slash:]
			}
		}
	}
	return ""
}
