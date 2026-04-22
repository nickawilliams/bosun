package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

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
// the adapter renders Block Kit blocks. When only Text is set, the adapter
// posts plain mrkdwn (editable in client).
type Content struct {
	Text     string       // Plain mrkdwn text (used when no block fields are set).
	Header   string       // PlainText header block (large bold text).
	Body     string       // mrkdwn section block (main content, below header).
	Actions  []CardButton // Link buttons rendered as an actions block.
	Table    []TableRow   // Info table rendered before cards.
	Fields   []Field      // Two-column key/value fields rendered as section fields.
	Sections []Section    // Per-item card blocks.
	Context  string       // mrkdwn context block (small muted text, after cards).
}

// TableRow is a row in an info table.
type TableRow struct {
	Cells []TableCell
}

// TableCell is a cell in a table row. Supports text with optional
// formatting, links, and emoji.
type TableCell struct {
	Text     string // Cell text content.
	Subtitle string // Second line of text (rendered after a newline).
	Emoji    string // Emoji shortcode (without colons), e.g., "jira".
	URL      string // If set, text is rendered as a link.
	Bold     bool
	Italic   bool
}

// Field is a key/value pair rendered in a two-column layout.
// Both Key and Value support mrkdwn (links, bold, code, etc.).
type Field struct {
	Key   string
	Value string
}

// CardButton is a link button rendered in a card's actions area.
type CardButton struct {
	Text  string // Button label.
	URL   string // Link URL.
	Style string // "primary" (green), "danger" (red), or "" (default).
}

// Section represents a card block in the notification.
type Section struct {
	Text     string       // Card title (mrkdwn).
	Subtitle string       // Card subtitle (mrkdwn).
	Body     string       // Card body (truncated to 200 chars by adapter).
	IconURL  string       // Small icon image URL.
	Buttons  []CardButton // Action buttons.
}

// HasBlocks returns true if any block-level fields are set.
func (c Content) HasBlocks() bool {
	return c.Header != "" || c.Body != "" || len(c.Actions) > 0 || len(c.Table) > 0 || len(c.Fields) > 0 || len(c.Sections) > 0 || c.Context != ""
}

// Item represents a single line item in a notification (one per repository).
type Item struct {
	Label     string // e.g., repository name: "my-service".
	URL       string // e.g., PR URL or release URL.
	Detail    string // e.g., "#42", "v1.2.3", "feature/X → main".
	Body      string // e.g., PR description (truncated for display).
	BranchURL string // e.g., GitHub branch tree URL.
}

// ThreadRef identifies an existing notification thread for replies.
type ThreadRef struct {
	Channel     string // Channel ID (resolved by adapter).
	Timestamp   string // Message timestamp (Slack thread_ts).
	ContentHash string // Hash of the message content (for skip-if-unchanged).
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

// ContentHash computes a short hash of the notification content for
// change detection.
func ContentHash(c Content) string {
	data, _ := json.Marshal(c)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
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
