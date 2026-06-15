package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
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
	path := fmt.Sprintf("/repos/%s/%s/pulls?head=%s:%s&state=all", owner, repository, owner, branch)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return code.PullRequest{}, fmt.Errorf("fetching PR for branch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var results []struct {
		Number   int     `json:"number"`
		Title    string  `json:"title"`
		Body     string  `json:"body"`
		HTMLURL  string  `json:"html_url"`
		State    string  `json:"state"`
		Draft    bool    `json:"draft"`
		MergedAt *string `json:"merged_at"`
		Head     struct {
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
		Number:  raw.Number,
		Title:   raw.Title,
		Body:    raw.Body,
		URL:     raw.HTMLURL,
		State:   state,
		BaseRef: raw.Base.Ref,
		HeadSHA: raw.Head.SHA,
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
	// signal and the extra fetches would be wasted.
	if state == "open" || state == "draft" {
		mergeable, err := a.fetchMergeableState(ctx, owner, repository, raw.Number)
		if err != nil {
			return pr, fmt.Errorf("fetching PR mergeable state: %w", err)
		}
		pr.MergeableState = mergeable

		review, err := a.fetchReviewDecision(ctx, owner, repository, raw.Number)
		if err != nil {
			return pr, fmt.Errorf("fetching PR review decision: %w", err)
		}
		pr.Review = review
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
// ones from the same person).
func (a *Adapter) fetchReviewDecision(ctx context.Context, owner, repository string, number int) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=100", owner, repository, number)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("parsing reviews: %w", err)
	}

	// Latest non-COMMENTED/PENDING review per user wins.
	latest := map[string]string{}
	for _, r := range reviews {
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
		return "changes_requested", nil
	case approved:
		return "approved", nil
	}

	// No decisive reviews — check if any reviewers were requested
	// (which would map to "awaiting"). The reviews endpoint doesn't
	// surface this; the PR detail's requested_reviewers does.
	requested, err := a.hasRequestedReviewers(ctx, owner, repository, number)
	if err != nil {
		return "", err
	}
	if requested {
		return "awaiting", nil
	}
	return "", nil
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
	path := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs?per_page=100", owner, repository, ref)
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.Release{}, fmt.Errorf("parsing release response: %w", err)
	}

	return code.Release{
		Tag: result.TagName,
		URL: result.HTMLURL,
	}, nil
}

var semverTag = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)

func (a *Adapter) GetLatestTag(ctx context.Context, owner, repository string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/tags?per_page=100", owner, repository)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("fetching tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", fmt.Errorf("parsing tags response: %w", err)
	}

	for _, t := range tags {
		if semverTag.MatchString(t.Name) {
			return t.Name, nil
		}
	}

	return "", nil
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
