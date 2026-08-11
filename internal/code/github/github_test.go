package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
					"requested_reviewers": []map[string]any{
						{"login": "alice"},
						{"login": "bob"},
					},
					"requested_teams": []map[string]any{
						{"slug": "backend"},
					},
					"assignees": []map[string]any{
						{"login": "carol"},
					},
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
	if got, want := pr.RequestedReviewers, []string{"alice", "bob"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RequestedReviewers = %v, want %v", got, want)
	}
	if got, want := pr.RequestedTeams, []string{"backend"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RequestedTeams = %v, want %v", got, want)
	}
	if got, want := pr.Assignees, []string{"carol"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Assignees = %v, want %v", got, want)
	}
}

func TestGetPRForBranchMerged(t *testing.T) {
	merged := "2024-01-01T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"number":            10,
				"title":             "Merged PR",
				"html_url":          "https://github.com/org/repo/pull/10",
				"state":             "closed",
				"merged_at":         merged,
				"merge_commit_sha":  "sq11122",
				"user":              map[string]any{"login": "alice"},
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
	// MergeCommitSHA is required by prerelease's sweep-up detection
	// for squash-merged PRs (local HEAD differs from the squash
	// commit on main; without this we'd never find a containing
	// release).
	if pr.MergeCommitSHA != "sq11122" {
		t.Errorf("MergeCommitSHA = %q, want %q", pr.MergeCommitSHA, "sq11122")
	}
	if pr.AuthorLogin != "alice" {
		t.Errorf("AuthorLogin = %q, want %q", pr.AuthorLogin, "alice")
	}
}

func TestGetPRForBranchPrefersOpen(t *testing.T) {
	// state=all returns a closed PR (older, listed first) and a newer
	// open one for the same branch — GetPRForBranch must pick the open.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/reviews"):
			_ = json.NewEncoder(w).Encode([]any{})
		case strings.HasSuffix(r.URL.Path, "/requested_reviewers"):
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []any{}, "teams": []any{}})
		case strings.Contains(r.URL.Path, "/pulls/8"):
			_ = json.NewEncoder(w).Encode(map[string]any{"mergeable_state": "clean"})
		default:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"number": 7, "state": "closed", "merged_at": nil,
					"html_url": "https://github.com/org/repo/pull/7"},
				{"number": 8, "state": "open", "merged_at": nil,
					"html_url": "https://github.com/org/repo/pull/8",
					"head":     map[string]any{"sha": "cafe"}},
			})
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	pr, err := a.GetPRForBranch(context.Background(), "org", "repo", "branch")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if pr.Number != 8 {
		t.Errorf("Number = %d, want 8 (the open PR, not the closed #7)", pr.Number)
	}
	if pr.State != "open" {
		t.Errorf("State = %q, want %q", pr.State, "open")
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
		if body["generate_release_notes"] != true {
			t.Errorf("generate_release_notes = %v, want true", body["generate_release_notes"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.4",
			"html_url": "https://github.com/org/repo/releases/tag/v1.2.4",
			"body":     "## What's Changed\n* something by @alice in https://github.com/org/repo/pull/1",
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	rel, err := a.CreateRelease(context.Background(), code.CreateReleaseRequest{
		Owner:         "org",
		Repository:    "repo",
		Tag:           "v1.2.4",
		Target:        "main",
		Name:          "v1.2.4",
		GenerateNotes: true,
	})
	if err != nil {
		t.Fatalf("CreateRelease() error: %v", err)
	}
	if rel.Tag != "v1.2.4" {
		t.Errorf("Tag = %q", rel.Tag)
	}
	if !strings.Contains(rel.Body, "What's Changed") {
		t.Errorf("Body = %q, want host-generated notes", rel.Body)
	}
	// CreateRelease runs PrettifyReleaseNotes on the API response so the
	// body crosses the code.Host boundary display-ready (covered in
	// detail by TestPrettifyReleaseNotes; smoke-checked here for the
	// adapter wiring).
	if !strings.Contains(rel.Body, "[@alice](https://github.com/alice)") {
		t.Errorf("mention not prettified.\nBody = %q", rel.Body)
	}
	if !strings.Contains(rel.Body, "[#1](https://github.com/org/repo/pull/1)") {
		t.Errorf("PR URL not prettified.\nBody = %q", rel.Body)
	}
}

func TestGetReleaseByTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo/releases/tags/v1.2.4" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v1.2.4",
			"html_url": "https://github.com/org/repo/releases/tag/v1.2.4",
			"body":     "## What's Changed\n* feat by @alice in https://github.com/org/repo/pull/1",
			"author":   map[string]any{"login": "alice"},
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	rel, err := a.GetReleaseByTag(context.Background(), "org", "repo", "v1.2.4")
	if err != nil {
		t.Fatalf("GetReleaseByTag() error: %v", err)
	}
	if rel.Tag != "v1.2.4" {
		t.Errorf("Tag = %q", rel.Tag)
	}
	if rel.AuthorLogin != "alice" {
		t.Errorf("AuthorLogin = %q, want alice", rel.AuthorLogin)
	}
	if !strings.Contains(rel.Body, "[@alice](https://github.com/alice)") {
		t.Errorf("body not prettified: %q", rel.Body)
	}
}

func TestGetReleaseByTagNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	_, err := a.GetReleaseByTag(context.Background(), "org", "repo", "missing")
	if !errors.Is(err, code.ErrNotFound) {
		t.Errorf("err = %v, want code.ErrNotFound", err)
	}
}

func TestPRsInRange(t *testing.T) {
	// /compare returns two commits; /commits/{sha}/pulls returns the
	// associated PR for each. PRsInRange should dedupe by PR number
	// (here both commits belong to the same PR — squash-merge with
	// the same merge SHA can be returned by multiple lookups in real
	// data) and filter out exclude numbers.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/compare/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commits": []map[string]any{
					{"sha": "aaa111"},
					{"sha": "bbb222"},
					{"sha": "ccc333"},
				},
			})
		case r.URL.Path == "/repos/org/repo/commits/aaa111/pulls":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":   42,
					"title":    "Add foo",
					"html_url": "https://github.com/org/repo/pull/42",
					"state":    "closed",
					"user":     map[string]any{"login": "alice"},
					"base":     map[string]any{"ref": "main"},
					"head":     map[string]any{"sha": "aaa111"},
				},
			})
		case r.URL.Path == "/repos/org/repo/commits/bbb222/pulls":
			// Same PR as aaa111 — dedup expected.
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":   42,
					"title":    "Add foo",
					"html_url": "https://github.com/org/repo/pull/42",
					"state":    "closed",
					"user":     map[string]any{"login": "alice"},
					"base":     map[string]any{"ref": "main"},
					"head":     map[string]any{"sha": "aaa111"},
				},
			})
		case r.URL.Path == "/repos/org/repo/commits/ccc333/pulls":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"number":   43,
					"title":    "Fix bar",
					"html_url": "https://github.com/org/repo/pull/43",
					"state":    "closed",
					"user":     map[string]any{"login": "bob"},
					"base":     map[string]any{"ref": "main"},
					"head":     map[string]any{"sha": "ccc333"},
				},
			})
		default:
			t.Errorf("unexpected path: %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	t.Run("dedupes by PR number", func(t *testing.T) {
		prs, err := a.PRsInRange(context.Background(), "org", "repo", "v1.0.0", "v1.1.0", nil)
		if err != nil {
			t.Fatalf("PRsInRange() error: %v", err)
		}
		if len(prs) != 2 {
			t.Fatalf("PRsInRange() = %d PRs, want 2 (#42 dedup'd)", len(prs))
		}
		if prs[0].Number != 42 || prs[0].AuthorLogin != "alice" {
			t.Errorf("first PR = %+v", prs[0])
		}
		if prs[1].Number != 43 || prs[1].AuthorLogin != "bob" {
			t.Errorf("second PR = %+v", prs[1])
		}
	})

	t.Run("respects exclude set", func(t *testing.T) {
		prs, err := a.PRsInRange(context.Background(), "org", "repo", "v1.0.0", "v1.1.0", []int{42})
		if err != nil {
			t.Fatalf("PRsInRange() error: %v", err)
		}
		if len(prs) != 1 || prs[0].Number != 43 {
			t.Errorf("PRsInRange() with #42 excluded = %+v, want only #43", prs)
		}
	})
}

func TestGetLatestTag(t *testing.T) {
	t.Run("prefers /releases/latest when present", func(t *testing.T) {
		// /releases/latest is the authoritative signal — it respects
		// the marked-as-latest flag maintainers can set. When this
		// endpoint succeeds, the /tags fallback isn't consulted.
		var tagsCalled bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/repos/org/repo/releases/latest":
				_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v2.5.0"})
			case strings.HasPrefix(r.URL.Path, "/repos/org/repo/tags"):
				tagsCalled = true
				_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "v9.9.9"}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		a := NewWithClient(server.Client(), server.URL, "token")
		tag, err := a.GetLatestTag(context.Background(), "org", "repo")
		if err != nil {
			t.Fatalf("GetLatestTag() error: %v", err)
		}
		if tag != "v2.5.0" {
			t.Errorf("Tag = %q, want %q (from /releases/latest)", tag, "v2.5.0")
		}
		if tagsCalled {
			t.Error("/tags should not have been called when /releases/latest succeeded")
		}
	})

	t.Run("falls back to /tags with semver-highest pick when no latest release", func(t *testing.T) {
		// Regression: GitHub's /tags returns tags in creation order,
		// not semver order. The old code returned whatever came
		// first in the response, which let a stale manually-pushed
		// tag shadow the actual highest version. Sort by semver and
		// pick the highest regardless of API order.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/repos/org/repo/releases/latest":
				w.WriteHeader(http.StatusNotFound)
			case strings.HasPrefix(r.URL.Path, "/repos/org/repo/tags"):
				// API returns v0.4.2399 FIRST (creation-order),
				// then v0.4.2510 — the real latest by semver.
				_ = json.NewEncoder(w).Encode([]map[string]string{
					{"name": "v0.4.2399"},
					{"name": "v0.4.2510"},
					{"name": "release-2024"}, // non-semver, skipped
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		a := NewWithClient(server.Client(), server.URL, "token")
		tag, err := a.GetLatestTag(context.Background(), "org", "repo")
		if err != nil {
			t.Fatalf("GetLatestTag() error: %v", err)
		}
		if tag != "v0.4.2510" {
			t.Errorf("Tag = %q, want %q (highest semver wins, not API order)", tag, "v0.4.2510")
		}
	})
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

func TestGetDefaultBranch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/org/repo" {
			t.Errorf("path = %q, want /repos/org/repo", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":           "repo",
			"default_branch": "develop",
		})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	branch, err := a.GetDefaultBranch(context.Background(), "org", "repo")
	if err != nil {
		t.Fatalf("GetDefaultBranch() error: %v", err)
	}
	if branch != "develop" {
		t.Errorf("GetDefaultBranch() = %q, want %q", branch, "develop")
	}
}

func TestGetDefaultBranchAPIError(t *testing.T) {
	// A repo the token can't see 404s. The base resolver treats any
	// error as "fall through to the local clone", so the only contract
	// here is that a failed request IS an error rather than a "" base.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	_, err := a.GetDefaultBranch(context.Background(), "org", "nonexistent")
	if err == nil {
		t.Fatal("GetDefaultBranch() error = nil, want an error for a 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}

func TestGetDefaultBranchMalformedResponse(t *testing.T) {
	// A 200 with a body that isn't the repository object — a proxy
	// error page, say. Decoding fails; that must surface rather than
	// leaving DefaultBranch zeroed and reporting "no default branch".
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	_, err := a.GetDefaultBranch(context.Background(), "org", "repo")
	if err == nil {
		t.Fatal("GetDefaultBranch() error = nil, want a decode error")
	}
	if !strings.Contains(err.Error(), "parsing repository response") {
		t.Errorf("error = %v, want it to name the parse failure", err)
	}
}

func TestGetDefaultBranchEmpty(t *testing.T) {
	// An empty repository reports no default branch. Handing "" back as
	// a PR base would produce a confusing 422 later, so it's an error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "repo"})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	if _, err := a.GetDefaultBranch(context.Background(), "org", "repo"); err == nil {
		t.Fatal("GetDefaultBranch() error = nil, want an error for a repo with no default branch")
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

func TestEditPR(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")
	err := a.EditPR(context.Background(), code.EditPRRequest{
		Owner: "org", Repository: "repo", Number: 42,
		Title: "New title", Body: "New body", Base: "develop",
	})
	if err != nil {
		t.Fatalf("EditPR() error: %v", err)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/repos/org/repo/pulls/42" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["title"] != "New title" || gotBody["body"] != "New body" || gotBody["base"] != "develop" {
		t.Errorf("body = %v", gotBody)
	}
}

func TestRemoveReviewers(t *testing.T) {
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
	err := a.RemoveReviewers(context.Background(), "org", "repo", 42, []string{"alice"}, []string{"backend"})
	if err != nil {
		t.Fatalf("RemoveReviewers() error: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/repos/org/repo/pulls/42/requested_reviewers" {
		t.Errorf("path = %q", gotPath)
	}
	if len(gotBody["reviewers"]) != 1 || gotBody["reviewers"][0] != "alice" {
		t.Errorf("reviewers = %v", gotBody["reviewers"])
	}
	if len(gotBody["team_reviewers"]) != 1 || gotBody["team_reviewers"][0] != "backend" {
		t.Errorf("team_reviewers = %v", gotBody["team_reviewers"])
	}
}

func TestRemoveAssignees(t *testing.T) {
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
	err := a.RemoveAssignees(context.Background(), "org", "repo", 42, []string{"charlie"})
	if err != nil {
		t.Fatalf("RemoveAssignees() error: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %q, want DELETE", gotMethod)
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

			got, reviewedBy, err := a.fetchReviewDecision(context.Background(), "org", "repo", 1)
			if err != nil {
				t.Fatalf("fetchReviewDecision() error: %v", err)
			}
			if got != tc.wantDecision {
				t.Errorf("decision = %q, want %q", got, tc.wantDecision)
			}
			// Every submitted (non-PENDING) review's author must land in
			// reviewedBy — reconciliation depends on it to not re-request
			// completed reviewers.
			seen := make(map[string]bool, len(reviewedBy))
			for _, u := range reviewedBy {
				seen[u] = true
			}
			for _, rv := range tc.reviews {
				state, _ := rv["state"].(string)
				user, _ := rv["user"].(map[string]any)
				login, _ := user["login"].(string)
				if state != "PENDING" && login != "" && !seen[login] {
					t.Errorf("reviewedBy = %v, missing submitter %q", reviewedBy, login)
				}
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

func TestGetLatestDeployment(t *testing.T) {
	// deployments newest-first; statuses keyed by deployment id.
	deployments := []map[string]any{
		{"id": 2, "sha": "shaFAIL", "ref": "v1.3.0", "created_at": "2026-07-15T10:00:00Z"},
		{"id": 1, "sha": "shaOK", "ref": "v1.2.3", "created_at": "2026-07-14T10:00:00Z"},
	}
	statuses := map[string]string{"2": "failure", "1": "success"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			if got := r.URL.Query().Get("environment"); got != "account-api-production" {
				t.Errorf("environment query = %q, want account-api-production", got)
			}
			_ = json.NewEncoder(w).Encode(deployments)
		case strings.Contains(r.URL.Path, "/deployments/") && strings.HasSuffix(r.URL.Path, "/statuses"):
			parts := strings.Split(r.URL.Path, "/")
			id := parts[len(parts)-2]
			_ = json.NewEncoder(w).Encode([]map[string]any{{"state": statuses[id]}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithClient(server.Client(), server.URL, "token")

	// Skips the failed newest deployment, returns the latest successful one.
	dep, err := a.GetLatestDeployment(context.Background(), "org", "repo", "account-api-production")
	if err != nil {
		t.Fatalf("GetLatestDeployment() error: %v", err)
	}
	if dep.Ref != "v1.2.3" || dep.SHA != "shaOK" || dep.State != "success" {
		t.Errorf("got %+v, want ref v1.2.3 / sha shaOK / state success", dep)
	}
}

func TestGetLatestDeploymentNoneSucceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "sha": "sha", "ref": "v1.2.3", "created_at": "2026-07-14T10:00:00Z"},
			})
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			_ = json.NewEncoder(w).Encode([]map[string]any{{"state": "failure"}})
		}
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	_, err := a.GetLatestDeployment(context.Background(), "org", "repo", "env")
	if !errors.Is(err, code.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetLatestDeploymentEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	_, err := a.GetLatestDeployment(context.Background(), "org", "repo", "env")
	if !errors.Is(err, code.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMergePR(t *testing.T) {
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/merge") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMethod, _ = body["merge_method"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "mergeSHA123", "merged": true})
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	sha, err := a.MergePR(context.Background(), "org", "repo", 42, "squash")
	if err != nil {
		t.Fatalf("MergePR() error: %v", err)
	}
	if sha != "mergeSHA123" {
		t.Errorf("sha = %q, want mergeSHA123", sha)
	}
	if gotMethod != "squash" {
		t.Errorf("merge_method = %q, want squash", gotMethod)
	}
}

// TestGetLatestDeploymentRepoNotFound locks the 404 semantics: a 404
// from /deployments means the repo wasn't found (a missing environment
// is a 200 with an empty list), so it must surface as an error —
// "repo unreachable" reading as ErrNotFound would classify a
// misconfigured repo as "never deployed" and dispatch a first deploy.
func TestGetLatestDeploymentRepoNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	_, err := a.GetLatestDeployment(context.Background(), "org", "gone", "env")
	if err == nil {
		t.Fatal("expected error for repo 404")
	}
	if errors.Is(err, code.ErrNotFound) {
		t.Errorf("repo 404 must not map to ErrNotFound; got %v", err)
	}
}

// TestGetLatestDeploymentPaginates walks past a first page of
// non-success deployments via the Link header to find the success on
// page two (regression: only the first 10 were ever inspected).
func TestGetLatestDeploymentPaginates(t *testing.T) {
	page1 := make([]map[string]any, 0, 30)
	for i := 60; i > 30; i-- {
		page1 = append(page1, map[string]any{
			"id": i, "sha": "shaFAIL", "ref": "v9.9.9", "created_at": "2026-07-15T10:00:00Z",
		})
	}
	page2 := []map[string]any{
		{"id": 1, "sha": "shaOK", "ref": "v1.2.3", "created_at": "2026-07-14T10:00:00Z"},
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			if r.URL.Query().Get("page") == "2" {
				_ = json.NewEncoder(w).Encode(page2)
				return
			}
			w.Header().Set("Link",
				"<"+server.URL+r.URL.Path+"?environment=env&per_page=30&page=2>; rel=\"next\"")
			_ = json.NewEncoder(w).Encode(page1)
		case strings.HasSuffix(r.URL.Path, "/statuses"):
			parts := strings.Split(r.URL.Path, "/")
			id := parts[len(parts)-2]
			state := "failure"
			if id == "1" {
				state = "success"
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{"state": state}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	dep, err := a.GetLatestDeployment(context.Background(), "org", "repo", "env")
	if err != nil {
		t.Fatalf("GetLatestDeployment() error: %v", err)
	}
	if dep.Ref != "v1.2.3" || dep.SHA != "shaOK" {
		t.Errorf("got %+v, want the page-two success (v1.2.3/shaOK)", dep)
	}
}

// TestPRsInRangePaginatesCompare locks the compare pagination: a range
// whose commit list spans multiple pages must surface PRs from the
// later pages (regression: only the default first page was read, so
// large ranges silently under-reported swept-in PRs).
func TestPRsInRangePaginatesCompare(t *testing.T) {
	page1 := make([]map[string]any, 0, 100)
	for i := 0; i < 100; i++ {
		page1 = append(page1, map[string]any{"sha": fmt.Sprintf("sha%03d", i)})
	}
	page2 := []map[string]any{{"sha": "shaLAST"}}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/compare/"):
			if r.URL.Query().Get("page") == "2" {
				_ = json.NewEncoder(w).Encode(map[string]any{"commits": page2})
				return
			}
			w.Header().Set("Link",
				"<"+server.URL+r.URL.Path+"?per_page=100&page=2>; rel=\"next\"")
			_ = json.NewEncoder(w).Encode(map[string]any{"commits": page1})
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			// Only the page-two commit has an associated PR.
			if strings.Contains(r.URL.Path, "shaLAST") {
				_ = json.NewEncoder(w).Encode([]map[string]any{{
					"number": 7, "title": "swept in", "state": "closed",
					"user": map[string]any{"login": "alice"},
				}})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	prs, err := a.PRsInRange(context.Background(), "org", "repo", "v1.0.0", "v2.0.0", nil)
	if err != nil {
		t.Fatalf("PRsInRange() error: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 7 {
		t.Fatalf("PRsInRange() = %+v, want the page-two PR #7", prs)
	}
}

// TestPRsInRangePropagatesLookupErrors locks the error classification:
// a 404 on a commit's /pulls means "no PR associated" and is skipped,
// but a rate-limit/auth class failure must propagate (regression: all
// errors were swallowed, so a rate-limited run returned an empty list
// with err == nil — read as "no extra PRs in this release").
func TestPRsInRangePropagatesLookupErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/compare/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commits": []map[string]any{{"sha": "aaa"}, {"sha": "bbb"}},
			})
		case strings.Contains(r.URL.Path, "/commits/aaa/"):
			http.NotFound(w, r) // rebased commit — skipped
		default:
			w.WriteHeader(http.StatusForbidden) // rate limited
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		}
	}))
	defer server.Close()
	a := NewWithClient(server.Client(), server.URL, "token")

	_, err := a.PRsInRange(context.Background(), "org", "repo", "v1.0.0", "v1.1.0", nil)
	if err == nil {
		t.Fatal("expected the rate-limit error to propagate")
	}
	if !strings.Contains(err.Error(), "bbb") {
		t.Errorf("err = %v, want the failing commit identified", err)
	}
}
