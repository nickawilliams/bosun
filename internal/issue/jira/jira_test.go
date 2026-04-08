package jira

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
)

func TestCreateIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issue":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			fields := body["fields"].(map[string]any)

			if fields["summary"] != "Test issue" {
				t.Errorf("summary = %v, want %q", fields["summary"], "Test issue")
			}
			project := fields["project"].(map[string]any)
			if project["key"] != "PROJ" {
				t.Errorf("project.key = %v, want %q", project["key"], "PROJ")
			}

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"key": "PROJ-42"})

		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/PROJ-42":
			json.NewEncoder(w).Encode(map[string]any{
				"key": "PROJ-42",
				"fields": map[string]any{
					"summary":   "Test issue",
					"status":    map[string]string{"name": "Ready"},
					"issuetype": map[string]string{"name": "Story"},
				},
			})
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	created, err := a.CreateIssue(context.Background(), issue.CreateRequest{
		Project: "PROJ",
		Title:   "Test issue",
		Type:    "story",
	})
	if err != nil {
		t.Fatalf("CreateIssue() error: %v", err)
	}
	if created.Key != "PROJ-42" {
		t.Errorf("Key = %q, want %q", created.Key, "PROJ-42")
	}
	if created.Title != "Test issue" {
		t.Errorf("Title = %q, want %q", created.Title, "Test issue")
	}
	if created.Status != "Ready" {
		t.Errorf("Status = %q, want %q", created.Status, "Ready")
	}
}

func TestGetIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"key": "PROJ-123",
			"fields": map[string]any{
				"summary":   "Add widget",
				"status":    map[string]string{"name": "In Progress"},
				"issuetype": map[string]string{"name": "Story"},
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	got, err := a.GetIssue(context.Background(), "PROJ-123")
	if err != nil {
		t.Fatalf("GetIssue() error: %v", err)
	}
	if got.Key != "PROJ-123" {
		t.Errorf("Key = %q, want %q", got.Key, "PROJ-123")
	}
	if got.Status != "In Progress" {
		t.Errorf("Status = %q, want %q", got.Status, "In Progress")
	}
	if got.URL != server.URL+"/browse/PROJ-123" {
		t.Errorf("URL = %q, want suffix /browse/PROJ-123", got.URL)
	}
}

func TestSetStatus(t *testing.T) {
	var transitionPosted string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{
				"transitions": []map[string]any{
					{"id": "11", "to": map[string]string{"name": "In Progress"}},
					{"id": "21", "to": map[string]string{"name": "Review"}},
				},
			})
		case r.Method == "POST":
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			transition := body["transition"].(map[string]any)
			transitionPosted = transition["id"].(string)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	err := a.SetStatus(context.Background(), "PROJ-123", "Review")
	if err != nil {
		t.Fatalf("SetStatus() error: %v", err)
	}
	if transitionPosted != "21" {
		t.Errorf("posted transition ID = %q, want %q", transitionPosted, "21")
	}
}

func TestSetStatusCaseInsensitive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{
				"transitions": []map[string]any{
					{"id": "11", "to": map[string]string{"name": "In Progress"}},
				},
			})
		case "POST":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	err := a.SetStatus(context.Background(), "PROJ-123", "in progress")
	if err != nil {
		t.Fatalf("SetStatus() should match case-insensitively: %v", err)
	}
}

func TestSetStatusTransitionNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"transitions": []map[string]any{
				{"id": "11", "to": map[string]string{"name": "In Progress"}},
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	err := a.SetStatus(context.Background(), "PROJ-123", "Done")
	if err == nil {
		t.Fatal("SetStatus() should error when transition not found")
	}
	if !strings.Contains(err.Error(), "In Progress") {
		t.Errorf("error should list available transitions, got: %v", err)
	}
}

func TestAuthHeader(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]any{
			"key":    "PROJ-1",
			"fields": map[string]any{
				"summary":   "x",
				"status":    map[string]string{"name": "Ready"},
				"issuetype": map[string]string{"name": "Story"},
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "user@example.com", "mytoken")
	a.GetIssue(context.Background(), "PROJ-1")

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user@example.com:mytoken"))
	if gotAuth != expected {
		t.Errorf("Authorization header = %q, want %q", gotAuth, expected)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"errorMessages":["Issue Does Not Exist"]}`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	_, err := a.GetIssue(context.Background(), "PROJ-999")
	if err == nil {
		t.Fatal("GetIssue() should error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}
