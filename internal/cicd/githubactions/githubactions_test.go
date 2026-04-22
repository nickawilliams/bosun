package githubactions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
)

func TestTriggerWorkflow(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	err := a.TriggerWorkflow(context.Background(), cicd.TriggerRequest{
		Owner:      "org",
		Repository: "repo",
		Workflow:   "deploy-preview.yml",
		Ref:        "feature/test",
		Inputs:     map[string]string{"issue": "PROJ-123"},
	})
	if err != nil {
		t.Fatalf("TriggerWorkflow() error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/repos/org/repo/actions/workflows/deploy-preview.yml/dispatches" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["ref"] != "feature/test" {
		t.Errorf("ref = %v, want %q", gotBody["ref"], "feature/test")
	}
	inputs, _ := gotBody["inputs"].(map[string]any)
	if inputs["issue"] != "PROJ-123" {
		t.Errorf("inputs.issue = %v, want %q", inputs["issue"], "PROJ-123")
	}
}

func TestTriggerWorkflowEmptyInputs(t *testing.T) {
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	err := a.TriggerWorkflow(context.Background(), cicd.TriggerRequest{
		Owner:      "org",
		Repository: "repo",
		Workflow:   "deploy.yml",
		Ref:        "main",
	})
	if err != nil {
		t.Fatalf("TriggerWorkflow() error: %v", err)
	}
	if gotBody["ref"] != "main" {
		t.Errorf("ref = %v, want %q", gotBody["ref"], "main")
	}
}

func TestTriggerWorkflowAuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "mytoken123")

	a.TriggerWorkflow(context.Background(), cicd.TriggerRequest{
		Owner: "org", Repository: "repo", Workflow: "deploy.yml", Ref: "main",
	})

	if gotAuth != "Bearer mytoken123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer mytoken123")
	}
}

func TestTriggerWorkflowAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	err := a.TriggerWorkflow(context.Background(), cicd.TriggerRequest{
		Owner: "org", Repository: "repo", Workflow: "nonexistent.yml", Ref: "main",
	})
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
