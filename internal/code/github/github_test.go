package github

import (
	"context"
	"encoding/json"
	"fmt"
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
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/pulls"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["title"] != "[PROJ-1] Test" {
				t.Errorf("title = %q, want %q", body["title"], "[PROJ-1] Test")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
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
		case r.Method == "POST":
			postCalled = true
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/reviews"):
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/requested_reviewers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []any{}, "teams": []any{}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/pulls/99"):
			// Single-PR detail call for mergeable_state.
			_ = json.NewEncoder(w).Encode(map[string]any{"mergeable_state": "clean"})
		case r.Method == "GET":
			// Existing PR found via list endpoint.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":    99,
					"title":     "Existing",
					"html_url":  "https://github.com/org/repo/pull/99",
					"state":     "open",
					"merged_at": nil,
					"head":      map[string]any{"sha": "abc123"},
				},
			})
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
		switch {
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"user": map[string]any{"login": "alice"}, "state": "APPROVED"},
			})
		case strings.HasSuffix(r.URL.Path, "/requested_reviewers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []any{}, "teams": []any{}})
		case strings.Contains(r.URL.Path, "/pulls/5"):
			// Single-PR detail call.
			_ = json.NewEncoder(w).Encode(map[string]any{"mergeable_state": "clean"})
		default:
			// List endpoint.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":    5,
					"title":     "My PR",
					"html_url":  "https://github.com/org/repo/pull/5",
					"state":     "open",
					"merged_at": nil,
					"head":      map[string]any{"sha": "deadbeef"},
				},
			})
		}
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
	if pr.HeadSHA != "deadbeef" {
		t.Errorf("HeadSHA = %q, want %q", pr.HeadSHA, "deadbeef")
	}
	if pr.MergeableState != "clean" {
		t.Errorf("MergeableState = %q, want %q", pr.MergeableState, "clean")
	}
	if pr.Review != "approved" {
		t.Errorf("Review = %q, want %q", pr.Review, "approved")
	}
}

func TestGetPRForBranchMerged(t *testing.T) {
	merged := "2024-01-01T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
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
		_ = json.NewEncoder(w).Encode([]any{})
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
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["tag_name"] != "v1.2.4" {
			t.Errorf("tag_name = %v, want v1.2.4", body["tag_name"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
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
		_ = json.NewEncoder(w).Encode([]map[string]string{
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
		_ = json.NewEncoder(w).Encode([]any{})
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

func TestListBranches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"name": "main"},
			{"name": "develop"},
			{"name": "feature/login"},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	branches, err := a.ListBranches(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("ListBranches() error: %v", err)
	}
	if len(branches) != 3 || branches[0] != "main" {
		t.Errorf("branches = %v", branches)
	}
}

func TestListBranchesPaginated(t *testing.T) {
	page := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		switch page {
		case 1:
			// GitHub uses /repositories/{id}/ in Link headers, not /repos/{owner}/{repo}/
			w.Header().Set("Link", fmt.Sprintf(
				`<%s/repositories/123/branches?per_page=2&page=2>; rel="next"`,
				"http://"+r.Host,
			))
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"name": "alpha"},
				{"name": "beta"},
			})
		case 2:
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"name": "main"},
			})
		default:
			t.Errorf("unexpected page %d", page)
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	branches, err := a.ListBranches(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("ListBranches() error: %v", err)
	}
	if len(branches) != 3 {
		t.Fatalf("got %d branches, want 3", len(branches))
	}
	if branches[2] != "main" {
		t.Errorf("branches = %v, want main on page 2", branches)
	}
}

func TestListCollaborators(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo/collaborators" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"login": "alice"},
			{"login": "bob"},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	logins, err := a.ListCollaborators(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("ListCollaborators() error: %v", err)
	}
	if len(logins) != 2 || logins[0] != "alice" {
		t.Errorf("logins = %v", logins)
	}
}

func TestListTeams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/myorg/teams" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{
			{"slug": "backend"},
			{"slug": "frontend"},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	teams, err := a.ListTeams(context.Background(), "myorg")
	if err != nil {
		t.Fatalf("ListTeams() error: %v", err)
	}
	if len(teams) != 2 || teams[0] != "backend" {
		t.Errorf("teams = %v", teams)
	}
}

func TestRequestReviewers(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string][]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	err := a.RequestReviewers(context.Background(), "org", "repo", 42, []string{"alice", "bob"}, []string{"backend"})
	if err != nil {
		t.Fatalf("RequestReviewers() error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/repos/org/repo/pulls/42/requested_reviewers" {
		t.Errorf("path = %q", gotPath)
	}
	if len(gotBody["reviewers"]) != 2 || gotBody["reviewers"][0] != "alice" {
		t.Errorf("reviewers = %v", gotBody["reviewers"])
	}
	if len(gotBody["team_reviewers"]) != 1 || gotBody["team_reviewers"][0] != "backend" {
		t.Errorf("team_reviewers = %v", gotBody["team_reviewers"])
	}
}

func TestAddAssignees(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string][]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	err := a.AddAssignees(context.Background(), "org", "repo", 42, []string{"charlie"})
	if err != nil {
		t.Fatalf("AddAssignees() error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/repos/org/repo/issues/42/assignees" {
		t.Errorf("path = %q", gotPath)
	}
	if len(gotBody["assignees"]) != 1 || gotBody["assignees"][0] != "charlie" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestGetAuthenticatedUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("path = %q, want /user", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"login": "octocat"})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	login, err := a.GetAuthenticatedUser(context.Background())
	if err != nil {
		t.Fatalf("GetAuthenticatedUser() error: %v", err)
	}
	if login != "octocat" {
		t.Errorf("login = %q, want %q", login, "octocat")
	}
}

func TestAuthHeader(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "mytoken123")
	_, _ = a.GetLatestTag(context.Background(), "org", "repo")

	if gotAuth != "Bearer mytoken123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer mytoken123")
	}
}

func TestGetChecks(t *testing.T) {
	cases := []struct {
		name      string
		runs      []map[string]any
		wantState string
		wantPass  int
		wantFail  int
		wantRun   int
	}{
		{
			name:      "empty",
			runs:      nil,
			wantState: "none",
		},
		{
			name: "all passing",
			runs: []map[string]any{
				{"status": "completed", "conclusion": "success"},
				{"status": "completed", "conclusion": "neutral"},
				{"status": "completed", "conclusion": "skipped"},
			},
			wantState: "passing",
			wantPass:  3,
		},
		{
			name: "mixed with failure",
			runs: []map[string]any{
				{"status": "completed", "conclusion": "success"},
				{"status": "completed", "conclusion": "failure"},
				{"status": "in_progress", "conclusion": nil},
			},
			wantState: "failing",
			wantPass:  1,
			wantFail:  1,
			wantRun:   1,
		},
		{
			name: "running with passes, no failures",
			runs: []map[string]any{
				{"status": "completed", "conclusion": "success"},
				{"status": "in_progress", "conclusion": nil},
			},
			wantState: "running",
			wantPass:  1,
			wantRun:   1,
		},
		{
			name: "action_required counts as failing",
			runs: []map[string]any{
				{"status": "completed", "conclusion": "action_required"},
			},
			wantState: "failing",
			wantFail:  1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"check_runs": tc.runs})
			}))
			defer server.Close()

			a := NewWithClient(server.Client(), server.URL, "token")

			rollup, err := a.GetChecks(context.Background(), "org", "repo", "abc123")
			if err != nil {
				t.Fatalf("GetChecks() error: %v", err)
			}
			if rollup.State != tc.wantState {
				t.Errorf("State = %q, want %q", rollup.State, tc.wantState)
			}
			if rollup.Passing != tc.wantPass {
				t.Errorf("Passing = %d, want %d", rollup.Passing, tc.wantPass)
			}
			if rollup.Failing != tc.wantFail {
				t.Errorf("Failing = %d, want %d", rollup.Failing, tc.wantFail)
			}
			if rollup.Running != tc.wantRun {
				t.Errorf("Running = %d, want %d", rollup.Running, tc.wantRun)
			}
		})
	}
}

func TestReviewDecision(t *testing.T) {
	cases := []struct {
		name             string
		reviews          []map[string]any
		requestedUsers   []string
		requestedTeams   []string
		wantDecision     string
	}{
		{
			name:         "approved by one",
			reviews:      []map[string]any{{"user": map[string]any{"login": "alice"}, "state": "APPROVED"}},
			wantDecision: "approved",
		},
		{
			name: "changes requested beats approval",
			reviews: []map[string]any{
				{"user": map[string]any{"login": "alice"}, "state": "APPROVED"},
				{"user": map[string]any{"login": "bob"}, "state": "CHANGES_REQUESTED"},
			},
			wantDecision: "changes_requested",
		},
		{
			name: "latest review per user wins",
			reviews: []map[string]any{
				{"user": map[string]any{"login": "alice"}, "state": "CHANGES_REQUESTED"},
				{"user": map[string]any{"login": "alice"}, "state": "APPROVED"},
			},
			wantDecision: "approved",
		},
		{
			name:         "no reviews but reviewers requested → awaiting",
			reviews:      []map[string]any{},
			requestedUsers: []string{"carol"},
			wantDecision: "awaiting",
		},
		{
			name:         "no reviews and no requests → empty",
			reviews:      []map[string]any{},
			wantDecision: "",
		},
		{
			name: "comments don't carry a decision",
			reviews: []map[string]any{
				{"user": map[string]any{"login": "alice"}, "state": "COMMENTED"},
			},
			wantDecision: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/reviews"):
					_ = json.NewEncoder(w).Encode(tc.reviews)
				case strings.HasSuffix(r.URL.Path, "/requested_reviewers"):
					users := make([]map[string]any, len(tc.requestedUsers))
					for i, u := range tc.requestedUsers {
						users[i] = map[string]any{"login": u}
					}
					teams := make([]map[string]any, len(tc.requestedTeams))
					for i, n := range tc.requestedTeams {
						teams[i] = map[string]any{"slug": n}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"users": users, "teams": teams,
					})
				}
			}))
			defer server.Close()

			a := NewWithClient(server.Client(), server.URL, "token")

			got, err := a.fetchReviewDecision(context.Background(), "org", "repo", 1)
			if err != nil {
				t.Fatalf("fetchReviewDecision() error: %v", err)
			}
			if got != tc.wantDecision {
				t.Errorf("decision = %q, want %q", got, tc.wantDecision)
			}
		})
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
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
