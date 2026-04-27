package issue

import (
	"context"
	"encoding/json"
)

// Issue represents an issue from a tracker.
type Issue struct {
	Key      string // e.g., "PROJ-123"
	Title    string
	Status   string // Current status name (e.g., "In Progress")
	StatusID string // Provider status ID (e.g., "10219")
	Type     string // e.g., "Story", "Bug"
	URL      string // Web link to the issue
}

// BoardColumn represents a column on an agile board.
type BoardColumn struct {
	Name      string   // Column display name (e.g., "Ready")
	StatusIDs []string // Status IDs mapped to this column
}

// Board represents an agile board.
type Board struct {
	ID   string // Board ID (e.g., "53")
	Name string // Board display name (e.g., "Bridge Builders")
	Type string // Board type (e.g., "scrum", "kanban")
}

// ListQuery defines filters for listing issues. All fields are
// optional — zero values are ignored. Adapters map these to their
// native query language (e.g., JQL for Jira).
type ListQuery struct {
	AssignedToMe  bool     // Filter to issues assigned to the authenticated user.
	Statuses      []string // Filter by status names (e.g., "Ready", "In Progress").
	Project       string   // Filter by project key (e.g., "PROJ").
	CurrentSprint bool     // Filter to the active sprint/iteration.
	MaxResults    int      // Limit results (0 = adapter default).
}

// CreateRequest holds the fields needed to create a new issue.
type CreateRequest struct {
	Project     string // Project key, e.g., "PROJ"
	Title       string
	Description string
	Type        string // "story" or "bug"
	Size        string // "small", "medium", "large"
}

// Tracker defines issue tracking operations needed by bosun.
type Tracker interface {
	// CreateIssue creates a new issue and returns it.
	CreateIssue(ctx context.Context, req CreateRequest) (Issue, error)

	// GetIssue retrieves an issue by key.
	GetIssue(ctx context.Context, issueKey string) (Issue, error)

	// SetStatus transitions an issue to the named status.
	// The adapter handles finding the correct transition.
	SetStatus(ctx context.Context, issueKey, statusName string) error

	// ListIssues returns issues matching the query, ordered by most
	// recently updated first.
	ListIssues(ctx context.Context, query ListQuery) ([]Issue, error)

	// BoardColumns returns the columns of an agile board in display
	// order (left to right). Each column contains the status IDs
	// mapped to it. Returns nil, nil if boardID is empty.
	BoardColumns(ctx context.Context, boardID string) ([]BoardColumn, error)

	// ListBoards returns boards visible to the current user.
	// If project is non-empty, results are filtered to boards
	// relevant to that project.
	ListBoards(ctx context.Context, project string) ([]Board, error)

	// GetProperty retrieves a stored property value from an issue.
	// Returns nil with no error if the property does not exist.
	GetProperty(ctx context.Context, issueKey string) (json.RawMessage, error)

	// SetProperty stores a property value on an issue. The value is
	// serialized as JSON. Use this for machine-readable metadata that
	// should not be visible to end users.
	SetProperty(ctx context.Context, issueKey string, value any) error
}
