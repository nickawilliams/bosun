// Package fakes provides in-memory implementations of bosun's
// capability interfaces (issue.Tracker, code.Host, etc.) for use in
// end-to-end command tests. Each fake records the calls made against
// it so tests can assert on side-effects without coupling to a real
// external service.
package fakes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/nickawilliams/bosun/internal/issue"
)

// Tracker is an in-memory issue.Tracker. Seed it with issues via
// SeedIssue before invoking commands; inspect Calls() and the issue
// map after to assert on behavior. Safe for concurrent use.
type Tracker struct {
	mu sync.Mutex

	// issues are keyed by issue key (e.g., "EX-123").
	issues map[string]issue.Issue
	// properties are stored per-issue-key JSON blobs.
	properties map[string]json.RawMessage
	// boards are returned by ListBoards.
	boards []issue.Board
	// boardColumns is keyed by board ID.
	boardColumns map[string][]issue.BoardColumn

	// CreateErr, GetErr, SetStatusErr override default behavior to
	// force error paths. nil means use the default success behavior.
	CreateErr    error
	GetErr       error
	SetStatusErr error

	// NewErr makes the harness's IssueTracker factory return this
	// error instead of the fake, simulating a provider that failed to
	// construct — bad credentials, incomplete config. It's a knob on
	// the fake rather than on the harness because the factory closure
	// reads it at call time, so a test can set it after New().
	NewErr error

	// calls records the method names invoked, in order, for tests that
	// want to assert on the call sequence.
	calls []string
}

// NewTracker constructs an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		issues:       map[string]issue.Issue{},
		properties:   map[string]json.RawMessage{},
		boardColumns: map[string][]issue.BoardColumn{},
	}
}

// SeedIssue registers an issue so GetIssue/ListIssues return it.
// Overwrites any prior entry with the same key.
func (t *Tracker) SeedIssue(iss issue.Issue) *Tracker {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.issues[iss.Key] = iss
	return t
}

// SeedBoard registers a board (and optionally its columns) for
// ListBoards/BoardColumns.
func (t *Tracker) SeedBoard(b issue.Board, columns ...issue.BoardColumn) *Tracker {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.boards = append(t.boards, b)
	if len(columns) > 0 {
		t.boardColumns[b.ID] = columns
	}
	return t
}

// Issue returns a snapshot of the issue under key, or (zero, false).
func (t *Tracker) Issue(key string) (issue.Issue, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	iss, ok := t.issues[key]
	return iss, ok
}

// Issues returns a snapshot of all stored issues, sorted by key.
func (t *Tracker) Issues() []issue.Issue {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]issue.Issue, 0, len(t.issues))
	for _, iss := range t.issues {
		out = append(out, iss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Calls returns a snapshot of method calls in invocation order.
func (t *Tracker) Calls() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, len(t.calls))
	copy(out, t.calls)
	return out
}

func (t *Tracker) recordCall(name string) {
	t.calls = append(t.calls, name)
}

// --- issue.Tracker implementation ---

func (t *Tracker) CreateIssue(_ context.Context, req issue.CreateRequest) (issue.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("CreateIssue")
	if t.CreateErr != nil {
		return issue.Issue{}, t.CreateErr
	}
	// Assign a key like PROJ-1, PROJ-2, ... based on prior issues in
	// the same project so multiple creates in a test get distinct keys.
	n := 1
	for k := range t.issues {
		if strings.HasPrefix(k, req.Project+"-") {
			n++
		}
	}
	iss := issue.Issue{
		Key:    fmt.Sprintf("%s-%d", req.Project, n),
		Title:  req.Title,
		Type:   req.Type,
		Status: "Open",
		URL:    fmt.Sprintf("https://tracker.test/browse/%s-%d", req.Project, n),
	}
	t.issues[iss.Key] = iss
	return iss, nil
}

func (t *Tracker) GetIssue(_ context.Context, key string) (issue.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("GetIssue")
	if t.GetErr != nil {
		return issue.Issue{}, t.GetErr
	}
	iss, ok := t.issues[key]
	if !ok {
		return issue.Issue{}, fmt.Errorf("issue %s not found", key)
	}
	return iss, nil
}

func (t *Tracker) SetStatus(_ context.Context, key, statusName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("SetStatus")
	if t.SetStatusErr != nil {
		return t.SetStatusErr
	}
	iss, ok := t.issues[key]
	if !ok {
		return fmt.Errorf("issue %s not found", key)
	}
	iss.Status = statusName
	t.issues[key] = iss
	return nil
}

func (t *Tracker) ListIssues(_ context.Context, query issue.ListQuery) ([]issue.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("ListIssues")
	var out []issue.Issue
	statusSet := map[string]bool{}
	for _, s := range query.Statuses {
		statusSet[s] = true
	}
	for _, iss := range t.issues {
		if query.Project != "" && !strings.HasPrefix(iss.Key, query.Project+"-") {
			continue
		}
		if len(statusSet) > 0 && !statusSet[iss.Status] {
			continue
		}
		out = append(out, iss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if query.MaxResults > 0 && len(out) > query.MaxResults {
		out = out[:query.MaxResults]
	}
	return out, nil
}

func (t *Tracker) BoardColumns(_ context.Context, boardID string) ([]issue.BoardColumn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("BoardColumns")
	if boardID == "" {
		return nil, nil
	}
	return t.boardColumns[boardID], nil
}

func (t *Tracker) ListBoards(_ context.Context, project string) ([]issue.Board, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("ListBoards")
	if project == "" {
		return append([]issue.Board(nil), t.boards...), nil
	}
	// Real Jira filters by project; the fake returns all boards regardless.
	// Tests that care about project filtering can override via a wrapper.
	return append([]issue.Board(nil), t.boards...), nil
}

func (t *Tracker) GetProperty(_ context.Context, key string) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("GetProperty")
	return t.properties[key], nil
}

func (t *Tracker) SetProperty(_ context.Context, key string, value any) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("SetProperty")
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	t.properties[key] = data
	return nil
}

func (t *Tracker) DeleteProperty(_ context.Context, key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordCall("DeleteProperty")
	delete(t.properties, key)
	return nil
}

// Verify Tracker satisfies issue.Tracker at compile time.
var _ issue.Tracker = (*Tracker)(nil)
