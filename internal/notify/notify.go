package notify

import "context"

// Message represents a notification to be sent to a channel.
type Message struct {
	Channel  string // Channel name (without #), e.g., "bb-prs".
	IssueKey string // e.g., "PROJ-123".
	Title    string // Issue title.
	IssueURL string // Link to the issue.
	Items    []Item // Per-repository details (PRs, releases, etc.).
	Summary  string // Short text for simple updates (e.g., preview status).
	Body     string // Template-rendered body (overrides default formatting when set).
}

// Item represents a single line item in a notification (one per repository).
type Item struct {
	Label  string // e.g., repository name: "my-service".
	URL    string // e.g., PR URL or release URL.
	Detail string // e.g., "#42", "v1.2.3", "feature/X → main".
}

// ThreadRef identifies an existing notification thread for replies.
type ThreadRef struct {
	Channel   string // Channel ID (resolved by adapter).
	Timestamp string // Message timestamp (Slack thread_ts).
}

// Notifier defines notification operations needed by bosun.
type Notifier interface {
	// Notify sends a new message to a channel. Returns a ThreadRef
	// that can be used for subsequent replies.
	Notify(ctx context.Context, msg Message) (ThreadRef, error)

	// FindThread searches a channel for an existing notification
	// containing the issue key. Returns a zero ThreadRef if not found.
	FindThread(ctx context.Context, channel, issueKey string) (ThreadRef, error)

	// ReplyToThread sends a reply to an existing notification thread.
	ReplyToThread(ctx context.Context, ref ThreadRef, msg Message) error
}
