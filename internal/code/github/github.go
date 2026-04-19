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

	body := map[string]string{
		"title": req.Title,
		"body":  req.Body,
		"head":  req.Head,
		"base":  req.Base,
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls", req.Owner, req.Repository)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return code.PullRequest{}, fmt.Errorf("creating PR: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return code.PullRequest{}, fmt.Errorf("parsing PR response: %w", err)
	}

	return code.PullRequest{
		Number: result.Number,
		Title:  result.Title,
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
	defer resp.Body.Close()

	var results []struct {
		Number   int    `json:"number"`
		Title    string `json:"title"`
		HTMLURL  string `json:"html_url"`
		State    string `json:"state"`
		MergedAt *string `json:"merged_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return code.PullRequest{}, fmt.Errorf("parsing PR list response: %w", err)
	}

	if len(results) == 0 {
		return code.PullRequest{}, nil
	}

	pr := results[0]
	state := pr.State
	if pr.MergedAt != nil {
		state = "merged"
	}

	return code.PullRequest{
		Number: pr.Number,
		Title:  pr.Title,
		URL:    pr.HTMLURL,
		State:  state,
	}, nil
}

func (a *Adapter) CreateRelease(ctx context.Context, req code.CreateReleaseRequest) (code.Release, error) {
	body := map[string]any{
		"tag_name":         req.Tag,
		"target_commitish": req.Target,
		"name":             req.Name,
		"body":             req.Body,
	}

	path := fmt.Sprintf("/repos/%s/%s/releases", req.Owner, req.Repository)
	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return code.Release{}, fmt.Errorf("creating release: %w", err)
	}
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github API error (HTTP %d): %s", resp.StatusCode, respBody)
	}

	return resp, nil
}
