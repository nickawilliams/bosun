package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/nickawilliams/bosun/internal/issue"
)

// Adapter implements issue.Tracker using the Jira REST API v3.
type Adapter struct {
	client  *http.Client
	baseURL string
	email   string
	token   string
}

// New returns a new Jira adapter.
func New(baseURL, email, token string) *Adapter {
	return &Adapter{
		client:  http.DefaultClient,
		baseURL: strings.TrimRight(baseURL, "/"),
		email:   email,
		token:   token,
	}
}

// NewWithClient returns a Jira adapter with a custom HTTP client (for testing).
func NewWithClient(client *http.Client, baseURL, email, token string) *Adapter {
	return &Adapter{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		email:   email,
		token:   token,
	}
}

func (a *Adapter) CreateIssue(ctx context.Context, req issue.CreateRequest) (issue.Issue, error) {
	body := map[string]any{
		"fields": map[string]any{
			"project":   map[string]string{"key": req.Project},
			"summary":   req.Title,
			"issuetype": map[string]string{"name": jiraIssueType(req.Type)},
			"description": adfDocument(req.Description),
		},
	}

	resp, err := a.doRequest(ctx, http.MethodPost, "/rest/api/3/issue", body)
	if err != nil {
		return issue.Issue{}, fmt.Errorf("creating issue: %w", err)
	}
	defer resp.Body.Close()

	var created struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return issue.Issue{}, fmt.Errorf("parsing create response: %w", err)
	}

	return a.GetIssue(ctx, created.Key)
}

func (a *Adapter) GetIssue(ctx context.Context, issueKey string) (issue.Issue, error) {
	path := fmt.Sprintf("/rest/api/3/issue/%s?fields=summary,status,issuetype", issueKey)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return issue.Issue{}, fmt.Errorf("getting issue %s: %w", issueKey, err)
	}
	defer resp.Body.Close()

	var result struct {
		Key    string `json:"key"`
		Fields struct {
			Summary   string `json:"summary"`
			Status    struct{ Name string } `json:"status"`
			IssueType struct{ Name string } `json:"issuetype"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return issue.Issue{}, fmt.Errorf("parsing issue response: %w", err)
	}

	return issue.Issue{
		Key:    result.Key,
		Title:  result.Fields.Summary,
		Status: result.Fields.Status.Name,
		Type:   result.Fields.IssueType.Name,
		URL:    a.baseURL + "/browse/" + result.Key,
	}, nil
}

func (a *Adapter) SetStatus(ctx context.Context, issueKey, statusName string) error {
	// Get available transitions.
	path := fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("getting transitions for %s: %w", issueKey, err)
	}
	defer resp.Body.Close()

	var result struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("parsing transitions response: %w", err)
	}

	// Find matching transition.
	var transitionID string
	var available []string
	for _, t := range result.Transitions {
		available = append(available, t.To.Name)
		if strings.EqualFold(t.To.Name, statusName) {
			transitionID = t.ID
			break
		}
	}

	if transitionID == "" {
		return fmt.Errorf(
			"no transition to %q available for %s (available: %s)",
			statusName, issueKey, strings.Join(available, ", "),
		)
	}

	// Perform transition.
	body := map[string]any{
		"transition": map[string]string{"id": transitionID},
	}
	resp2, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return fmt.Errorf("transitioning %s to %q: %w", issueKey, statusName, err)
	}
	resp2.Body.Close()

	return nil
}

func (a *Adapter) ListIssues(ctx context.Context, query issue.ListQuery) ([]issue.Issue, error) {
	jql := buildJQL(query)

	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	path := fmt.Sprintf("/rest/api/3/search/jql?jql=%s&fields=summary,status,issuetype&maxResults=%d",
		url.QueryEscape(jql), maxResults)

	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("searching issues: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary   string                 `json:"summary"`
				Status    struct{ Name string }  `json:"status"`
				IssueType struct{ Name string }  `json:"issuetype"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	issues := make([]issue.Issue, len(result.Issues))
	for i, r := range result.Issues {
		issues[i] = issue.Issue{
			Key:    r.Key,
			Title:  r.Fields.Summary,
			Status: r.Fields.Status.Name,
			Type:   r.Fields.IssueType.Name,
			URL:    a.baseURL + "/browse/" + r.Key,
		}
	}
	return issues, nil
}

// buildJQL assembles a JQL query string from the given ListQuery filters.
func buildJQL(query issue.ListQuery) string {
	clauses := []string{"resolution = Unresolved"}

	if query.AssignedToMe {
		clauses = append(clauses, "assignee = currentUser()")
	}
	if len(query.Statuses) > 0 {
		quoted := make([]string, len(query.Statuses))
		for i, s := range query.Statuses {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		clauses = append(clauses, "status IN ("+strings.Join(quoted, ", ")+")")
	}
	if query.Project != "" {
		clauses = append(clauses, fmt.Sprintf("project = %q", query.Project))
	}
	if query.CurrentSprint {
		clauses = append(clauses, "sprint IN openSprints()")
	}

	return strings.Join(clauses, " AND ") + " ORDER BY updated DESC"
}

// doRequest executes an authenticated request against the Jira API.
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

	// Basic auth: base64(email:token).
	auth := base64.StdEncoding.EncodeToString([]byte(a.email + ":" + a.token))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jira API error (HTTP %d): %s", resp.StatusCode, body)
	}

	return resp, nil
}

// jiraIssueType maps bosun issue types to Jira issue type names.
func jiraIssueType(t string) string {
	switch strings.ToLower(t) {
	case "bug":
		return "Bug"
	case "story":
		return "Story"
	case "task":
		return "Task"
	default:
		return "Story"
	}
}

// adfDocument wraps plain text in a minimal Atlassian Document Format document.
func adfDocument(text string) map[string]any {
	if text == "" {
		return nil
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": []map[string]any{
			{
				"type": "paragraph",
				"content": []map[string]any{
					{
						"type": "text",
						"text": text,
					},
				},
			},
		},
	}
}
