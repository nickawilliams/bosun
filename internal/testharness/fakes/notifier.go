package fakes

import (
	"context"
	"fmt"
	"sync"

	"github.com/nickawilliams/bosun/internal/notify"
)

// Notifier is an in-memory notify.Notifier. Seed threads and
// announcements before invoking commands; inspect Messages() and
// Calls() after to assert on what was posted. Safe for concurrent use.
type Notifier struct {
	mu sync.Mutex

	// messages records every Notify call's message, in order.
	messages []notify.Message
	// replies records every ReplyToThread call, in order.
	replies []notify.Message
	// threads are keyed by "channel|issueKey" for FindThread.
	threads map[string]notify.ThreadRef
	// announcements are keyed by "channel|query" for HasAnnouncement.
	announcements map[string]bool

	// NotifyErr and HasAnnouncementErr override default behavior to
	// force error paths. nil means success.
	NotifyErr          error
	HasAnnouncementErr error

	// calls records the method names invoked, in order.
	calls []string
}

// NewNotifier constructs an empty Notifier.
func NewNotifier() *Notifier {
	return &Notifier{
		threads:       map[string]notify.ThreadRef{},
		announcements: map[string]bool{},
	}
}

// SeedThread registers an existing notification thread so FindThread
// returns it for the channel + issue key.
func (n *Notifier) SeedThread(channel, issueKey string, ref notify.ThreadRef) *Notifier {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.threads[channel+"|"+issueKey] = ref
	return n
}

// SeedAnnouncement marks a query (typically a release URL) as already
// announced in a channel, so HasAnnouncement reports true for it.
func (n *Notifier) SeedAnnouncement(channel, query string) *Notifier {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.announcements[channel+"|"+query] = true
	return n
}

// Messages returns a snapshot of messages sent via Notify, in order.
func (n *Notifier) Messages() []notify.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Message, len(n.messages))
	copy(out, n.messages)
	return out
}

// Replies returns a snapshot of messages sent via ReplyToThread.
func (n *Notifier) Replies() []notify.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Message, len(n.replies))
	copy(out, n.replies)
	return out
}

// Calls returns a snapshot of method calls in invocation order.
func (n *Notifier) Calls() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.calls))
	copy(out, n.calls)
	return out
}

func (n *Notifier) recordCall(name string) {
	n.calls = append(n.calls, name)
}

// --- notify.Notifier implementation ---

func (n *Notifier) AuthTest(_ context.Context) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("AuthTest")
	return "bosun-test", nil
}

func (n *Notifier) Notify(_ context.Context, msg notify.Message) (notify.ThreadRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("Notify")
	if n.NotifyErr != nil {
		return notify.ThreadRef{}, n.NotifyErr
	}
	n.messages = append(n.messages, msg)
	ref := notify.ThreadRef{
		Channel:   msg.Channel,
		Timestamp: fmt.Sprintf("1700000000.%06d", len(n.messages)),
	}
	n.threads[msg.Channel+"|"+msg.IssueKey] = ref
	return ref, nil
}

func (n *Notifier) FindThread(_ context.Context, channel, issueKey string) (notify.ThreadRef, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("FindThread")
	return n.threads[channel+"|"+issueKey], nil
}

func (n *Notifier) HasAnnouncement(_ context.Context, channel, query, _ string) (bool, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("HasAnnouncement")
	if n.HasAnnouncementErr != nil {
		return false, n.HasAnnouncementErr
	}
	return n.announcements[channel+"|"+query], nil
}

func (n *Notifier) ReplyToThread(_ context.Context, _ notify.ThreadRef, msg notify.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("ReplyToThread")
	n.replies = append(n.replies, msg)
	return nil
}

func (n *Notifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.recordCall("Close")
}

// Verify Notifier satisfies notify.Notifier at compile time.
var _ notify.Notifier = (*Notifier)(nil)
