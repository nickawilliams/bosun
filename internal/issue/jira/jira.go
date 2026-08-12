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
	"time"

	"github.com/nickawilliams/bosun/internal/issue"
)

// requestTimeout bounds a single HTTP request as a backstop for
// callers that pass a context carrying no deadline. The caller's
// context stays the primary bound — every request is issued with
// http.NewRequestWithContext, so a caller's deadline still cancels an
// in-flight request earlier than this. The backstop only bites when
// there is no deadline to honor, which is what keeps an unreachable
// host from hanging such a caller indefinitely.
const requestTimeout = 30 * time.Second

// defaultClient is the adapter's own client rather than
// http.DefaultClient, which carries no timeout and is shared
// process-wide (mutating it would reach into unrelated callers).
var defaultClient = &http.Client{Timeout: requestTimeout}

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
		client:  defaultClient,
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
			"project":     map[string]string{"key": req.Project},
			"summary":     req.Title,
			"issuetype":   map[string]string{"name": jiraIssueType(req.Type)},
			"description": adfDocument(req.Description),
		},
	}

	resp, err := a.doRequest(ctx, http.MethodPost, "/rest/api/3/issue", body)
	if err != nil {
		return issue.Issue{}, fmt.Errorf("creating issue: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var created struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return issue.Issue{}, fmt.Errorf("parsing create response: %w", err)
	}

	return a.GetIssue(ctx, created.Key)
}

func (a *Adapter) GetIssue(ctx context.Context, issueKey string) (issue.Issue, error) {
	path := fmt.Sprintf("/rest/api/3/issue/%s?fields=summary,status,issuetype,description", issueKey)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return issue.Issue{}, fmt.Errorf("getting issue %s: %w", issueKey, issue.ErrNotFound)
		}
		return issue.Issue{}, fmt.Errorf("getting issue %s: %w", issueKey, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string                `json:"summary"`
			Description map[string]any        `json:"description"`
			Status      struct{ Name string } `json:"status"`
			IssueType   struct {
				Name    string `json:"name"`
				IconURL string `json:"iconUrl"`
			} `json:"issuetype"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return issue.Issue{}, fmt.Errorf("parsing issue response: %w", err)
	}

	return issue.Issue{
		Key:         result.Key,
		Title:       result.Fields.Summary,
		Description: adfPlainText(result.Fields.Description),
		Status:      result.Fields.Status.Name,
		Type:        result.Fields.IssueType.Name,
		TypeIconURL: a.typeIconURL(result.Fields.IssueType.IconURL),
		URL:         a.baseURL + "/browse/" + result.Key,
	}, nil
}

// jiraDefaultIssueTypeAvatars maps Jira's built-in issue types to their
// platform-default universal_avatar IDs. Built-in types (always Epic, and
// on older instances Story/Task/Bug/Sub-task) expose only a legacy
// /images/icons/issuetypes/<name>.svg icon with no avatar record. These
// IDs are Jira Cloud platform defaults — stable across instances, each
// verified against the live avatar endpoint — used to upgrade the small
// legacy icon to the full-size avatar.
var jiraDefaultIssueTypeAvatars = map[string]string{
	"epic":     "10307",
	"story":    "10315",
	"task":     "10318",
	"bug":      "10303",
	"subtask":  "10316",
	"sub-task": "10316",
}

// typeIconURL upgrades a legacy issue-type icon to its full-size
// universal_avatar when the type is a known Jira built-in. Most issue
// types already point iconUrl at a universal_avatar (returned unchanged);
// only the legacy /images/icons/issuetypes/<name>.svg system icons —
// which are a fixed ~16px and have no avatar record — are remapped.
func (a *Adapter) typeIconURL(iconURL string) string {
	const legacyPrefix = "/images/icons/issuetypes/"
	_, after, found := strings.Cut(iconURL, legacyPrefix)
	if !found {
		return iconURL
	}
	name := strings.TrimSuffix(strings.ToLower(after), ".svg")
	if id, ok := jiraDefaultIssueTypeAvatars[name]; ok {
		return a.baseURL + "/rest/api/2/universal_avatar/view/type/issuetype/avatar/" + id
	}
	return iconURL
}

func (a *Adapter) SetStatus(ctx context.Context, issueKey, statusName string) error {
	// Get available transitions.
	path := fmt.Sprintf("/rest/api/3/issue/%s/transitions", issueKey)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("getting transitions for %s: %w", issueKey, err)
	}
	defer func() { _ = resp.Body.Close() }()

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
	_ = resp2.Body.Close()

	return nil
}

func (a *Adapter) ListIssues(ctx context.Context, query issue.ListQuery) ([]issue.Issue, error) {
	jql := buildJQL(query)

	maxResults := query.MaxResults
	if maxResults <= 0 {
		maxResults = 200
	}

	path := fmt.Sprintf("/rest/api/3/search/jql?jql=%s&fields=summary,status,issuetype&maxResults=%d",
		url.QueryEscape(jql), maxResults)

	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("searching issues: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Issues []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"status"`
				IssueType struct{ Name string } `json:"issuetype"`
			} `json:"fields"`
		} `json:"issues"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	issues := make([]issue.Issue, len(result.Issues))
	for i, r := range result.Issues {
		issues[i] = issue.Issue{
			Key:      r.Key,
			Title:    r.Fields.Summary,
			Status:   r.Fields.Status.Name,
			StatusID: r.Fields.Status.ID,
			Type:     r.Fields.IssueType.Name,
			URL:      a.baseURL + "/browse/" + r.Key,
		}
	}
	return issues, nil
}

func (a *Adapter) ListBoards(ctx context.Context, project string) ([]issue.Board, error) {
	path := "/rest/agile/1.0/board?maxResults=100"
	if project != "" {
		path += "&projectKeyOrId=" + url.QueryEscape(project)
	}

	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("listing boards: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Values []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing boards response: %w", err)
	}

	boards := make([]issue.Board, len(result.Values))
	for i, b := range result.Values {
		boards[i] = issue.Board{
			ID:   fmt.Sprintf("%d", b.ID),
			Name: b.Name,
			Type: b.Type,
		}
	}
	return boards, nil
}

func (a *Adapter) BoardColumns(ctx context.Context, boardID string) ([]issue.BoardColumn, error) {
	if boardID == "" {
		return nil, nil
	}

	path := fmt.Sprintf("/rest/agile/1.0/board/%s/configuration", boardID)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("getting board configuration: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ColumnConfig struct {
			Columns []struct {
				Name     string `json:"name"`
				Statuses []struct {
					ID string `json:"id"`
				} `json:"statuses"`
			} `json:"columns"`
		} `json:"columnConfig"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing board configuration: %w", err)
	}

	columns := make([]issue.BoardColumn, len(result.ColumnConfig.Columns))
	for i, col := range result.ColumnConfig.Columns {
		ids := make([]string, len(col.Statuses))
		for j, s := range col.Statuses {
			ids[j] = s.ID
		}
		columns[i] = issue.BoardColumn{
			Name:      col.Name,
			StatusIDs: ids,
		}
	}
	return columns, nil
}

const propertyKey = "bosun"

func (a *Adapter) GetProperty(ctx context.Context, issueKey string) (json.RawMessage, error) {
	path := fmt.Sprintf("/rest/api/3/issue/%s/properties/%s", issueKey, propertyKey)
	resp, err := a.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		// 404 means property doesn't exist yet — not an error.
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil, nil
		}
		return nil, fmt.Errorf("getting property for %s: %w", issueKey, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing property response: %w", err)
	}
	return result.Value, nil
}

func (a *Adapter) SetProperty(ctx context.Context, issueKey string, value any) error {
	path := fmt.Sprintf("/rest/api/3/issue/%s/properties/%s", issueKey, propertyKey)
	resp, err := a.doRequest(ctx, http.MethodPut, path, value)
	if err != nil {
		return fmt.Errorf("setting property for %s: %w", issueKey, err)
	}
	_ = resp.Body.Close()
	return nil
}

func (a *Adapter) DeleteProperty(ctx context.Context, issueKey string) error {
	path := fmt.Sprintf("/rest/api/3/issue/%s/properties/%s", issueKey, propertyKey)
	resp, err := a.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		// 404 means the property already doesn't exist — treat as success.
		if strings.Contains(err.Error(), "HTTP 404") {
			return nil
		}
		return fmt.Errorf("deleting property for %s: %w", issueKey, err)
	}
	_ = resp.Body.Close()
	return nil
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

	return strings.Join(clauses, " AND ") + " ORDER BY statusCategory ASC, updated DESC"
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
		defer func() { _ = resp.Body.Close() }()
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

// adfPlainText extracts readable plain text from an ADF (Atlassian Document
// Format) document. Walks the node tree, concatenating "text" nodes and
// inserting paragraph breaks between block-level nodes. Lossy by design —
// notification cards need a short readable summary, not faithful markup.
func adfPlainText(doc map[string]any) string {
	if len(doc) == 0 {
		return ""
	}
	var b strings.Builder
	adfWalk(doc, &b)
	return strings.TrimSpace(b.String())
}

func adfWalk(node map[string]any, b *strings.Builder) {
	switch node["type"] {
	case "text":
		if t, ok := node["text"].(string); ok {
			b.WriteString(t)
		}
		return
	case "hardBreak":
		b.WriteString("\n")
		return
	}

	content, _ := node["content"].([]any)
	for _, child := range content {
		if m, ok := child.(map[string]any); ok {
			adfWalk(m, b)
		}
	}

	switch node["type"] {
	case "paragraph", "heading", "bulletList", "orderedList", "listItem", "blockquote", "codeBlock":
		b.WriteString("\n")
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
