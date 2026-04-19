package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

func TestCreatePR(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/pulls"):
			// No existing PR.
			json.NewEncoder(w).Encode([]any{})
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/pulls"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["title"] != "[PROJ-1] Test" {
				t.Errorf("title = %q, want %q", body["title"], "[PROJ-1] Test")
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"number":   42,
				"title":    body["title"],
				"html_url": "https://github.com/org/repo/pull/42",
				"state":    "open",
			})
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.CreatePR(context.Background(), code.CreatePRRequest{
		Owner: "org",
		Repository: "repo",
		Head:  "feature/test",
		Base:  "main",
		Title: "[PROJ-1] Test",
	})
	if err != nil {
		t.Fatalf("CreatePR() error: %v", err)
	}
	if pr.Number != 42 {
		t.Errorf("Number = %d, want 42", pr.Number)
	}
	if pr.URL != "https://github.com/org/repo/pull/42" {
		t.Errorf("URL = %q", pr.URL)
	}
}

func TestCreatePRIdempotent(t *testing.T) {
	postCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET":
			// Existing PR found.
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":    99,
					"title":     "Existing",
					"html_url":  "https://github.com/org/repo/pull/99",
					"state":     "open",
					"merged_at": nil,
				},
			})
		case r.Method == "POST":
			postCalled = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.CreatePR(context.Background(), code.CreatePRRequest{
		Owner: "org", Repository: "repo", Head: "branch", Base: "main", Title: "New",
	})
	if err != nil {
		t.Fatalf("CreatePR() error: %v", err)
	}
	if pr.Number != 99 {
		t.Errorf("should return existing PR, got Number=%d", pr.Number)
	}
	if postCalled {
		t.Error("should not POST when existing PR found")
	}
}

func TestGetPRForBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":    5,
				"title":     "My PR",
				"html_url":  "https://github.com/org/repo/pull/5",
				"state":     "open",
				"merged_at": nil,
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.GetPRForBranch(context.Background(), "org", "repo", "feature/test")
	if err != nil {
		t.Fatalf("GetPRForBranch() error: %v", err)
	}
	if pr.Number != 5 {
		t.Errorf("Number = %d, want 5", pr.Number)
	}
	if pr.State != "open" {
		t.Errorf("State = %q, want %q", pr.State, "open")
	}
}

func TestGetPRForBranchMerged(t *testing.T) {
	merged := "2024-01-01T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":    10,
				"title":     "Merged PR",
				"html_url":  "https://github.com/org/repo/pull/10",
				"state":     "closed",
				"merged_at": merged,
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.GetPRForBranch(context.Background(), "org", "repo", "branch")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pr.State != "merged" {
		t.Errorf("State = %q, want %q", pr.State, "merged")
	}
}

func TestGetPRForBranchNone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.GetPRForBranch(context.Background(), "org", "repo", "branch")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pr.Number != 0 {
		t.Errorf("Number = %d, want 0 (no PR)", pr.Number)
	}
}

func TestCreateRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["tag_name"] != "v1.2.4" {
			t.Errorf("tag_name = %v, want v1.2.4", body["tag_name"])
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.4",
			"html_url": "https://github.com/org/repo/releases/tag/v1.2.4",
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	rel, err := a.CreateRelease(context.Background(), code.CreateReleaseRequest{
		Owner:  "org",
		Repository: "repo",
		Tag:    "v1.2.4",
		Target: "main",
		Name:   "v1.2.4",
	})
	if err != nil {
		t.Fatalf("CreateRelease() error: %v", err)
	}
	if rel.Tag != "v1.2.4" {
		t.Errorf("Tag = %q", rel.Tag)
	}
}

func TestGetLatestTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]string{
			{"name": "release-2024"},
			{"name": "v1.5.2"},
			{"name": "v1.5.1"},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	tag, err := a.GetLatestTag(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("GetLatestTag() error: %v", err)
	}
	if tag != "v1.5.2" {
		t.Errorf("Tag = %q, want %q (should skip non-semver)", tag, "v1.5.2")
	}
}

func TestGetLatestTagEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	tag, err := a.GetLatestTag(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tag != "" {
		t.Errorf("Tag = %q, want empty", tag)
	}
}

func TestAuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "mytoken123")
	a.GetLatestTag(context.Background(), "org", "repo")

	if gotAuth != "Bearer mytoken123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer mytoken123")
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	_, err := a.GetLatestTag(context.Background(), "org", "nonexistent")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}
