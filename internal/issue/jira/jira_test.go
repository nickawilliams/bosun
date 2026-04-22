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

func TestListIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/rest/api/3/search/jql") {
			t.Errorf("path = %s, want /rest/api/3/search/jql", r.URL.Path)
		}
		jql := r.URL.Query().Get("jql")
		if !strings.Contains(jql, "assignee = currentUser()") {
			t.Errorf("jql = %q, want to contain assignee clause", jql)
		}

		json.NewEncoder(w).Encode(map[string]any{
			"issues": []map[string]any{
				{
					"key": "PROJ-10",
					"fields": map[string]any{
						"summary":   "First issue",
						"status":    map[string]any{"id": "3", "name": "In Progress"},
						"issuetype": map[string]string{"name": "Story"},
					},
				},
				{
					"key": "PROJ-20",
					"fields": map[string]any{
						"summary":   "Second issue",
						"status":    map[string]any{"id": "10219", "name": "Ready"},
						"issuetype": map[string]string{"name": "Bug"},
					},
				},
			},
			"total":      2,
			"maxResults": 50,
			"startAt":    0,
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	issues, err := a.ListIssues(context.Background(), issue.ListQuery{AssignedToMe: true})
	if err != nil {
		t.Fatalf("ListIssues() error: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].Key != "PROJ-10" {
		t.Errorf("issues[0].Key = %q, want %q", issues[0].Key, "PROJ-10")
	}
	if issues[0].Status != "In Progress" {
		t.Errorf("issues[0].Status = %q, want %q", issues[0].Status, "In Progress")
	}
	if issues[0].StatusID != "3" {
		t.Errorf("issues[0].StatusID = %q, want %q", issues[0].StatusID, "3")
	}
	if issues[1].Title != "Second issue" {
		t.Errorf("issues[1].Title = %q, want %q", issues[1].Title, "Second issue")
	}
	if issues[1].StatusID != "10219" {
		t.Errorf("issues[1].StatusID = %q, want %q", issues[1].StatusID, "10219")
	}
	if issues[0].URL != server.URL+"/browse/PROJ-10" {
		t.Errorf("issues[0].URL = %q, want suffix /browse/PROJ-10", issues[0].URL)
	}
}

func TestListIssuesWithFilters(t *testing.T) {
	var gotJQL string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotJQL = r.URL.Query().Get("jql")
		json.NewEncoder(w).Encode(map[string]any{
			"issues":     []map[string]any{},
			"total":      0,
			"maxResults": 50,
			"startAt":    0,
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	a.ListIssues(context.Background(), issue.ListQuery{
		AssignedToMe:  true,
		Statuses:      []string{"Ready", "In Progress"},
		Project:       "PROJ",
		CurrentSprint: true,
	})

	if !strings.Contains(gotJQL, "assignee = currentUser()") {
		t.Errorf("jql missing assignee clause: %q", gotJQL)
	}
	if !strings.Contains(gotJQL, `status IN (`) {
		t.Errorf("jql missing status clause: %q", gotJQL)
	}
	if !strings.Contains(gotJQL, "Ready") {
		t.Errorf("jql missing Ready status: %q", gotJQL)
	}
	if !strings.Contains(gotJQL, `project = `) {
		t.Errorf("jql missing project clause: %q", gotJQL)
	}
	if !strings.Contains(gotJQL, "sprint IN openSprints()") {
		t.Errorf("jql missing sprint clause: %q", gotJQL)
	}
}

func TestListIssuesEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issues":     []map[string]any{},
			"total":      0,
			"maxResults": 50,
			"startAt":    0,
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	issues, err := a.ListIssues(context.Background(), issue.ListQuery{AssignedToMe: true})
	if err != nil {
		t.Fatalf("ListIssues() error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("got %d issues, want 0", len(issues))
	}
}

func TestBuildJQL(t *testing.T) {
	tests := []struct {
		name  string
		query issue.ListQuery
		want  []string // substrings that must appear
	}{
		{
			name:  "empty query",
			query: issue.ListQuery{},
			want:  []string{"resolution = Unresolved", "ORDER BY statusCategory ASC, updated DESC"},
		},
		{
			name:  "assigned to me",
			query: issue.ListQuery{AssignedToMe: true},
			want:  []string{"assignee = currentUser()", "resolution = Unresolved"},
		},
		{
			name:  "status filter",
			query: issue.ListQuery{Statuses: []string{"Ready"}},
			want:  []string{`status IN ("Ready")`},
		},
		{
			name:  "multiple statuses",
			query: issue.ListQuery{Statuses: []string{"Ready", "In Progress"}},
			want:  []string{`status IN ("Ready", "In Progress")`},
		},
		{
			name:  "project filter",
			query: issue.ListQuery{Project: "PROJ"},
			want:  []string{`project = "PROJ"`},
		},
		{
			name:  "current sprint",
			query: issue.ListQuery{CurrentSprint: true},
			want:  []string{"sprint IN openSprints()"},
		},
		{
			name: "all filters",
			query: issue.ListQuery{
				AssignedToMe:  true,
				Statuses:      []string{"Ready"},
				Project:       "PROJ",
				CurrentSprint: true,
			},
			want: []string{
				"assignee = currentUser()",
				`status IN ("Ready")`,
				`project = "PROJ"`,
				"sprint IN openSprints()",
				"resolution = Unresolved",
				"ORDER BY statusCategory ASC, updated DESC",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildJQL(tt.query)
			for _, sub := range tt.want {
				if !strings.Contains(got, sub) {
					t.Errorf("buildJQL() = %q, want to contain %q", got, sub)
				}
			}
		})
	}
}

func TestBoardColumns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/agile/1.0/board/53/configuration" {
			t.Errorf("path = %s, want /rest/agile/1.0/board/53/configuration", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"columnConfig": map[string]any{
				"columns": []map[string]any{
					{
						"name": "Ready",
						"statuses": []map[string]string{
							{"id": "10219"},
							{"id": "10210"},
						},
					},
					{
						"name": "In Progress",
						"statuses": []map[string]string{
							{"id": "3"},
						},
					},
					{
						"name": "Done",
						"statuses": []map[string]string{
							{"id": "10002"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	cols, err := a.BoardColumns(context.Background(), "53")
	if err != nil {
		t.Fatalf("BoardColumns() error: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("got %d columns, want 3", len(cols))
	}
	if cols[0].Name != "Ready" {
		t.Errorf("cols[0].Name = %q, want %q", cols[0].Name, "Ready")
	}
	if len(cols[0].StatusIDs) != 2 {
		t.Fatalf("cols[0] has %d statuses, want 2", len(cols[0].StatusIDs))
	}
	if cols[0].StatusIDs[0] != "10219" {
		t.Errorf("cols[0].StatusIDs[0] = %q, want %q", cols[0].StatusIDs[0], "10219")
	}
	if cols[0].StatusIDs[1] != "10210" {
		t.Errorf("cols[0].StatusIDs[1] = %q, want %q", cols[0].StatusIDs[1], "10210")
	}
	if cols[1].Name != "In Progress" {
		t.Errorf("cols[1].Name = %q, want %q", cols[1].Name, "In Progress")
	}
}

func TestBoardColumnsEmptyID(t *testing.T) {
	a := NewWithClient(http.DefaultClient, "http://unused", "e", "t")

	cols, err := a.BoardColumns(context.Background(), "")
	if err != nil {
		t.Fatalf("BoardColumns() error: %v", err)
	}
	if cols != nil {
		t.Errorf("expected nil columns for empty board ID, got %v", cols)
	}
}

func TestListBoards(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		json.NewEncoder(w).Encode(map[string]any{
			"values": []map[string]any{
				{"id": 53, "name": "Bridge Builders", "type": "scrum"},
				{"id": 12, "name": "Kanban Board", "type": "kanban"},
			},
			"isLast": true,
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")

	boards, err := a.ListBoards(context.Background(), "EX")
	if err != nil {
		t.Fatalf("ListBoards() error: %v", err)
	}
	if !strings.Contains(gotPath, "projectKeyOrId=EX") {
		t.Errorf("path = %q, want projectKeyOrId=EX", gotPath)
	}
	if len(boards) != 2 {
		t.Fatalf("got %d boards, want 2", len(boards))
	}
	if boards[0].ID != "53" {
		t.Errorf("boards[0].ID = %q, want %q", boards[0].ID, "53")
	}
	if boards[0].Name != "Bridge Builders" {
		t.Errorf("boards[0].Name = %q, want %q", boards[0].Name, "Bridge Builders")
	}
	if boards[0].Type != "scrum" {
		t.Errorf("boards[0].Type = %q, want %q", boards[0].Type, "scrum")
	}
}

func TestListBoardsNoProject(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		json.NewEncoder(w).Encode(map[string]any{
			"values": []map[string]any{},
			"isLast": true,
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "test@test.com", "token")
	a.ListBoards(context.Background(), "")

	if strings.Contains(gotPath, "projectKeyOrId") {
		t.Errorf("path = %q, should not contain projectKeyOrId when empty", gotPath)
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
