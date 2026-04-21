package notify

import "context"

// Message represents a notification to be sent to a channel.
type Message struct {
	Channel  string  // Channel name (without #), e.g., "bb-prs".
	IssueKey string  // e.g., "PROJ-123".
	Title    string  // Issue title.
	IssueURL string  // Link to the issue.
	Items    []Item  // Per-repository details (PRs, releases, etc.).
	Content  Content // Rendered notification content.
}

// Content holds the rendered notification text. When Block fields are set,
// the adapter renders Block Kit blocks (header + section + context). When
// only Text is set, the adapter posts plain mrkdwn (editable in client).
type Content struct {
	Text    string // Plain mrkdwn text (used when no block fields are set).
	Header  string // PlainText header block (large bold text).
	Body    string // mrkdwn section block (main content).
	Context string // mrkdwn context block (small muted text).
}

// HasBlocks returns true if any block-level fields are set.
func (c Content) HasBlocks() bool {
	return c.Header != "" || c.Body != "" || c.Context != ""
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
	// AuthTest verifies that the credentials are valid and returns the
	// authenticated user/bot name.
	AuthTest(ctx context.Context) (string, error)

	// Notify sends a new message to a channel. Returns a ThreadRef
	// that can be used for subsequent replies.
	Notify(ctx context.Context, msg Message) (ThreadRef, error)

	// FindThread searches a channel for an existing notification
	// containing the issue key. Returns a zero ThreadRef if not found.
	FindThread(ctx context.Context, channel, issueKey string) (ThreadRef, error)

	// ReplyToThread sends a reply to an existing notification thread.
	ReplyToThread(ctx context.Context, ref ThreadRef, msg Message) error

	// Close persists any cached state. Should be called when the notifier
	// is no longer needed.
	Close()
}

type noCacheKey struct{}

// WithNoCache returns a context that tells the notifier to bypass cached
// results and hit the API directly.
func WithNoCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, noCacheKey{}, true)
}

// NoCache reports whether the context requests a cache bypass.
func NoCache(ctx context.Context) bool {
	_, ok := ctx.Value(noCacheKey{}).(bool)
	return ok
}
