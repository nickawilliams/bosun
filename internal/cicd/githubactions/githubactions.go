package githubactions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nickawilliams/bosun/internal/cicd"
)

// Adapter implements cicd.CICD using the GitHub Actions API.
type Adapter struct {
	client  *http.Client
	baseURL string
	token   string
}

// New creates an Adapter with the given GitHub PAT.
func New(token string) *Adapter {
	return &Adapter{
		client:  http.DefaultClient,
		baseURL: "https://api.github.com",
		token:   token,
	}
}

// NewWithClient creates an Adapter with a custom HTTP client and base URL
// for testing.
func NewWithClient(client *http.Client, baseURL, token string) *Adapter {
	return &Adapter{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

// TriggerWorkflow dispatches a GitHub Actions workflow run.
func (a *Adapter) TriggerWorkflow(ctx context.Context, req cicd.TriggerRequest) error {
	path := fmt.Sprintf("/repos/%s/%s/actions/workflows/%s/dispatches",
		req.Owner, req.Repository, req.Workflow)

	body := map[string]any{
		"ref":    req.Ref,
		"inputs": req.Inputs,
	}

	resp, err := a.doRequest(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

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
		return nil, fmt.Errorf("github actions API error (HTTP %d): %s", resp.StatusCode, respBody)
	}

	return resp, nil
}
