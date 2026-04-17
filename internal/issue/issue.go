package issue

import "context"

// Issue represents an issue from a tracker.
type Issue struct {
	Key    string // e.g., "PROJ-123"
	Title  string
	Status string // Current status name (e.g., "In Progress")
	Type   string // e.g., "Story", "Bug"
	URL    string // Web link to the issue
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
}
