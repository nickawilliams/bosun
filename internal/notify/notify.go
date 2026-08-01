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

// Content is a provider-agnostic notification. It carries structured data
// (the issue, per-repo items, an optional preview deployment) plus
// pre-rendered override strings from the user's templates — but no
// provider-specific presentation. Each adapter renders it into its own
// shape: the Slack adapter builds Block Kit cards; a plain-text provider
// would render prose. When only Text is set, the adapter posts it as-is
// (standard Markdown, editable in client).
type Content struct {
	Text    string      `json:",omitempty"` // Full pre-rendered body (flat-text path, e.g. release).
	Header  string      `json:",omitempty"` // Rendered header override (structured path).
	Body    string      `json:",omitempty"` // Rendered body override (structured path).
	Context string      `json:",omitempty"` // Rendered footer/context override (structured path).
	Issue   *IssueRef   `json:",omitempty"` // Tracker ticket the notification is about.
	Items   []Item      `json:",omitempty"` // Per-repository items (PRs, releases).
	Preview *PreviewRef `json:",omitempty"` // Ephemeral preview deployment.
	IconURL string      `json:",omitempty"` // Author avatar (raw URL) for item cards.
}

// IssueRef is the tracker ticket a notification is about. The adapter
// normalizes IconURL for its own image proxy.
type IssueRef struct {
	Key         string // e.g., "PROJ-123".
	Title       string // Issue summary.
	Type        string // e.g., "Story", "Bug"; adapters default to "Issue".
	URL         string // Link to the issue.
	Description string // Issue body, plain. Empty when the tracker has none.
	IconURL     string // Raw issue-type icon URL; adapter normalizes it.
}

// PreviewRef is an ephemeral preview deployment.
type PreviewRef struct {
	Name string // Environment name (e.g., "brave-falcon").
	URL  string // Rendered preview environment URL.
}

// Structured reports whether the content carries structured data or
// override strings a provider should render into rich presentation. When
// false, only Text is set and the adapter posts it as plain Markdown.
func (c Content) Structured() bool {
	return c.Issue != nil || len(c.Items) > 0 || c.Preview != nil ||
		c.Header != "" || c.Body != "" || c.Context != ""
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

	// HasAnnouncement searches a channel for any recent message
	// containing query (typically a release URL). Returns true when
	// at least one match is found in the channel's recent history.
	// Used by prerelease to detect whether a release-cut-by-someone-
	// else has already been announced before posting a duplicate.
	//
	// excludeIssueKey scopes the dedup to messages NOT bearing that
	// issue key in their bosun metadata — letting same-workspace
	// re-runs fall through to Notify, which upserts the user's own
	// prior message. Empty string means "match any message".
	//
	// Failures (missing scopes, network errors) return false + the
	// error so callers can soft-fail; a false result on error keeps
	// the announcement path conservative ("post when in doubt").
	HasAnnouncement(ctx context.Context, channel, query, excludeIssueKey string) (bool, error)

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
