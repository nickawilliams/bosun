package issue

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nickawilliams/bosun/internal/provider"
)

// ConfigGroup is the config group that holds issue-tracker settings.
// Adapters compose their own keys against it (issue_tracker.base_url)
// rather than repeating the literal.
const ConfigGroup = "issue_tracker"

// ErrNotFound reports that the tracker definitively knows no issue by
// the given key — as opposed to a transient failure where the issue's
// existence couldn't be determined. Callers that mutate state keyed on
// the issue (start's workspace provisioning) branch on this to abort
// before creating anything for a typo'd key, while degrading
// gracefully on mere connectivity problems.
var ErrNotFound = errors.New("issue not found")

// Issue represents an issue from a tracker.
type Issue struct {
	Key         string // e.g., "PROJ-123"
	Title       string
	Description string // Plain-text issue body. Empty when the tracker has none.
	Status      string // Current status name (e.g., "In Progress")
	StatusID    string // Provider status ID (e.g., "10219")
	Type        string // e.g., "Story", "Bug"
	TypeIconURL string // Issue-type icon image URL. Empty when none.
	URL         string // Web link to the issue
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
	Type        string // "story" | "bug" | "task"
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

	// DeleteProperty removes a stored property from an issue. A missing
	// property is not an error.
	DeleteProperty(ctx context.Context, issueKey string) error

	// AuthTest verifies the tracker's credentials and returns a display
	// string identifying what it authenticated to (e.g.
	// "jira → acme.atlassian.net (dev@acme.test)"). The whole string is
	// the provider's, because a tracker's identity has no shape bosun
	// can assume: one provider identifies by site and account, another
	// by workspace, another by nothing at all.
	AuthTest(ctx context.Context) (string, error)
}

// TrackerDescriptor is what an issue-tracker provider package
// contributes to bosun: the config value that selects it, the
// configuration it needs, and how to build it. The services registry is
// the only thing that collects descriptors; nothing else knows which
// providers exist.
type TrackerDescriptor struct {
	// Name is the value that selects this provider in config
	// (issue_tracker.provider), e.g. "jira".
	Name string

	// Keys are the provider-specific config keys under the
	// "issue_tracker" group, relative to it (e.g. "base_url"). They are
	// spliced into bosun's config schema so init, doctor, and
	// `config check` cover them without knowing which provider is
	// configured. Keys shared by every tracker (provider, project,
	// board_id, the status mappings) stay in the schema proper.
	Keys []provider.ConfigKey

	// ParseIdentifier extracts this provider's issue key from an
	// arbitrary string — a branch name, a workspace directory, a commit
	// subject — and returns "" when the string carries none. Key shape is
	// provider knowledge (Jira's PROJ-123 is not GitHub Issues' #123).
	//
	// It hangs off the descriptor rather than the Tracker interface
	// because it is pure grammar: no credentials, no network, nothing to
	// construct. The CLI parses keys on display paths that run for every
	// command — the breadcrumb reads the issue out of the branch name —
	// and requiring a live tracker there would put a credential prompt in
	// front of commands that never touch the tracker at all.
	ParseIdentifier func(s string) string

	// New constructs the tracker from configuration.
	New func(provider.Config) (Tracker, error)
}
