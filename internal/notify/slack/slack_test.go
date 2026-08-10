package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/notify"
	slackapi "github.com/slack-go/slack"
)

func TestNotify(t *testing.T) {
	var postedChannel, postedText string
	var postedBlocks bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C123", "name": "bb-prs"},
				},
			})
		case "/conversations.history":
			// No existing notification — the upsert requires a
			// successful history read before it may post fresh.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{},
			})
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "user_id": "U-SELF",
			})
		case "/chat.postMessage":
			postedChannel = r.FormValue("channel")
			postedText = r.FormValue("text")
			if r.FormValue("blocks") != "" {
				postedBlocks = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"channel": "C123",
				"ts":      "1234567890.123456",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.Notify(context.Background(), notify.Message{
		Channel:  "bb-prs",
		IssueKey: "PROJ-123",
		Title:    "Add widget",
		IssueURL: "https://jira.example.com/browse/PROJ-123",
		Items: []notify.Item{
			{Label: "my-service", URL: "https://github.com/org/my-service/pull/42", Detail: "#42"},
			{Label: "my-frontend", URL: "https://github.com/org/my-frontend/pull/43", Detail: "#43"},
		},
		Content: notify.Content{
			Header: "PROJ-123: Add widget",
			Body:   "PROJ-123 is ready for review",
		},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}

	if ref.Channel != "C123" {
		t.Errorf("Channel = %q, want %q", ref.Channel, "C123")
	}
	if ref.Timestamp != "1234567890.123456" {
		t.Errorf("Timestamp = %q, want %q", ref.Timestamp, "1234567890.123456")
	}
	if postedChannel != "C123" {
		t.Errorf("posted to channel %q, want %q", postedChannel, "C123")
	}
	if postedText == "" {
		t.Error("fallback text should not be empty")
	}
	if !postedBlocks {
		t.Error("expected blocks to be posted")
	}
}

// TestForceHardLineBreaks locks the line-break conversion the markdown
// block path applies — non-empty lines get a two-space hard-break
// marker, blank lines are left alone so paragraph breaks (`\n\n`)
// survive. Matches mrkdwn's "every \n is visible" intuition.
func TestForceHardLineBreaks(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"single line untouched", "one line", "one line  "},
		{"two non-empty lines get hard breaks", "a\nb", "a  \nb  "},
		{"paragraph break preserved", "a\n\nb", "a  \n\nb  "},
		{
			name: "release-notes shape",
			in:   "going out `r`: u\nWhat's Changed\n* item\n\n**Footer**",
			want: "going out `r`: u  \nWhat's Changed  \n* item  \n\n**Footer**  ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forceHardLineBreaks(tt.in); got != tt.want {
				t.Errorf("\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestNotifyTextSendsAsMarkdownText confirms that Content.Text (the
// no-block-fields path) is sent via Slack's top-level `markdown_text`
// parameter — Slack's native standard-Markdown renderer, supported on
// both chat.postMessage and chat.update (whereas the Block Kit markdown
// block is rejected by chat.update). The body is hard-break-converted so
// each `\n` renders as a visible line break.
//
// markdown_text is documented as mutually exclusive with `text` and
// `blocks`, so neither is set on this path.
func TestNotifyTextSendsAsMarkdownText(t *testing.T) {
	var postedMarkdown, postedText, postedBlocks string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C1", "name": "ch"}},
			})
		case "/chat.postMessage":
			postedMarkdown = r.FormValue("markdown_text")
			postedText = r.FormValue("text")
			postedBlocks = r.FormValue("blocks")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"channel": "C1",
				"ts":      "1.1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("t", slackapi.OptionAPIURL(server.URL+"/"))
	body := "## What's Changed\n* alpha\n* beta\n\n**Full Changelog**: x...y"
	_, err := a.Notify(context.Background(), notify.Message{
		Channel: "ch", Content: notify.Content{Text: body},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if postedMarkdown != forceHardLineBreaks(body) {
		t.Errorf("markdown_text = %q, want hard-break form", postedMarkdown)
	}
	if postedText != "" {
		t.Errorf("text should be empty (mutually exclusive with markdown_text); got %q", postedText)
	}
	if postedBlocks != "" {
		t.Errorf("blocks should be empty; got %q", postedBlocks)
	}
}

func TestNotifyChannelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	_, err := a.Notify(context.Background(), notify.Message{
		Channel:  "nonexistent",
		IssueKey: "PROJ-1",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent channel")
	}
	if got := err.Error(); got != `channel "nonexistent" not found` {
		t.Errorf("error = %q, want channel not found message", got)
	}
}

func TestFindThread(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C123", "name": "bb-prs"},
				},
			})
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "unrelated message", "ts": "1111111111.111111"},
					{"text": "PROJ-123: Add widget", "ts": "2222222222.222222"},
					{"text": "another message", "ts": "3333333333.333333"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.FindThread(context.Background(), "bb-prs", "PROJ-123")
	if err != nil {
		t.Fatalf("FindThread() error: %v", err)
	}
	if ref.Channel != "C123" {
		t.Errorf("Channel = %q, want %q", ref.Channel, "C123")
	}
	if ref.Timestamp != "2222222222.222222" {
		t.Errorf("Timestamp = %q, want %q", ref.Timestamp, "2222222222.222222")
	}
}

func TestFindThreadNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C123", "name": "bb-prs"},
				},
			})
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "unrelated message", "ts": "1111111111.111111"},
					{"text": "something else", "ts": "2222222222.222222"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.FindThread(context.Background(), "bb-prs", "PROJ-999")
	if err != nil {
		t.Fatalf("FindThread() error: %v", err)
	}
	if ref.Timestamp != "" {
		t.Errorf("expected zero ThreadRef, got Timestamp=%q", ref.Timestamp)
	}
	if ref.Channel != "" {
		t.Errorf("expected zero ThreadRef, got Channel=%q", ref.Channel)
	}
}

func TestHasAnnouncement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "going out `extracker`: https://github.com/org/repo/releases/tag/v1.2.4", "ts": "111.111"},
					{"text": "unrelated chatter", "ts": "222.222"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	t.Run("URL present in channel history → true", func(t *testing.T) {
		found, err := a.HasAnnouncement(context.Background(), "release-coordination",
			"https://github.com/org/repo/releases/tag/v1.2.4", "")
		if err != nil {
			t.Fatalf("HasAnnouncement() error: %v", err)
		}
		if !found {
			t.Error("expected true (URL is in the first message)")
		}
	})

	t.Run("URL absent → false", func(t *testing.T) {
		found, err := a.HasAnnouncement(context.Background(), "release-coordination",
			"https://github.com/org/repo/releases/tag/v9.9.9", "")
		if err != nil {
			t.Fatalf("HasAnnouncement() error: %v", err)
		}
		if found {
			t.Error("expected false (URL is not in any message)")
		}
	})

	t.Run("empty query → (false, nil) without a network call", func(t *testing.T) {
		found, err := a.HasAnnouncement(context.Background(), "release-coordination", "", "")
		if err != nil {
			t.Fatalf("HasAnnouncement(empty) error: %v", err)
		}
		if found {
			t.Error("expected false on empty query")
		}
	})
}

func TestReplyToThread(t *testing.T) {
	var postedChannel, postedThreadTS string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/chat.postMessage":
			postedChannel = r.FormValue("channel")
			postedThreadTS = r.FormValue("thread_ts")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"channel": "C123",
				"ts":      "3333333333.333333",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	err := a.ReplyToThread(context.Background(),
		notify.ThreadRef{Channel: "C123", Timestamp: "2222222222.222222"},
		notify.Message{
			IssueKey: "PROJ-123",
			Content:  notify.Content{Text: "Preview deployment requested for PROJ-123"},
		},
	)
	if err != nil {
		t.Fatalf("ReplyToThread() error: %v", err)
	}
	if postedChannel != "C123" {
		t.Errorf("channel = %q, want %q", postedChannel, "C123")
	}
	if postedThreadTS != "2222222222.222222" {
		t.Errorf("thread_ts = %q, want %q", postedThreadTS, "2222222222.222222")
	}
}

// TestNotifyUpsertDeleteFallback locks the prerelease re-run flow:
// when a prior announcement exists and chat.update fails (in practice
// Slack returns internal_error for markdown_text updates, despite the
// docs listing it as supported), the adapter falls back to
// chat.delete + chat.postMessage so the upsert still completes. The
// caller gets back the NEW message's timestamp, not the deleted one.
func TestNotifyUpsertDeleteFallback(t *testing.T) {
	var (
		updateCalled bool
		deleteCalled bool
		deletedTS    string
		postCount    int
		postedText   string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "user_id": "U-SELF",
			})
		case "/conversations.history":
			// Existing message keyed on bosun metadata for PROJ-123,
			// authored by this principal (the metadata pass is
			// author-scoped).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{
						"text": "going out `extracker`: https://github.com/org/repo/releases/tag/v1.2.3",
						"ts":   "1111111111.111111",
						"user": "U-SELF",
						"metadata": map[string]any{
							"event_type":    "bosun_notification",
							"event_payload": map[string]any{"issue_key": "PROJ-123"},
						},
					},
				},
			})
		case "/chat.update":
			updateCalled = true
			// Mimic the markdown_text-on-update failure that motivates the fallback.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": "internal_error",
			})
		case "/chat.delete":
			deleteCalled = true
			deletedTS = r.FormValue("ts")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": deletedTS, "channel": "C123"})
		case "/chat.postMessage":
			postCount++
			postedText = r.FormValue("markdown_text")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"channel": "C123",
				"ts":      "9999999999.999999",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.Notify(context.Background(), notify.Message{
		Channel:  "release-coordination",
		IssueKey: "PROJ-123",
		Content: notify.Content{
			Text: "going out `extracker`: https://github.com/org/repo/releases/tag/v1.2.4",
		},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}

	if !updateCalled {
		t.Error("expected chat.update to be attempted first")
	}
	if !deleteCalled {
		t.Error("expected chat.delete fallback after update failure")
	}
	if deletedTS != "1111111111.111111" {
		t.Errorf("deleted ts = %q, want the existing message's ts", deletedTS)
	}
	if postCount != 1 {
		t.Errorf("expected exactly one post-fresh call, got %d", postCount)
	}
	if !strings.Contains(postedText, "v1.2.4") {
		t.Errorf("posted body missing new version, got %q", postedText)
	}
	if ref.Timestamp != "9999999999.999999" {
		t.Errorf("returned Timestamp = %q, want the new post's ts", ref.Timestamp)
	}
}

// TestNotifyIgnoresUnownedMessages locks the author-scoped upsert match.
// A message that mentions the issue key but was authored by someone other
// than bosun (and has no bosun metadata) is NOT touched — we don't risk a
// cant_update_message / cant_delete_message on a stale text match. Notify
// posts fresh; the unrelated message stays untouched.
func TestNotifyIgnoresUnownedMessages(t *testing.T) {
	var (
		updateCalled bool
		deleteCalled bool
		postCount    int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "USELF"})
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/conversations.history":
			// A message mentioning the issue key but authored by someone
			// else (UOTHER) and WITHOUT bosun metadata. The upsert path
			// must not touch it.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "anything about PROJ-123 here", "ts": "1111111111.111111", "user": "UOTHER"},
				},
			})
		case "/chat.update":
			updateCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "cant_update_message"})
		case "/chat.delete":
			deleteCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "cant_delete_message"})
		case "/chat.postMessage":
			postCount++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "channel": "C123", "ts": "9999999999.999999",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.Notify(context.Background(), notify.Message{
		Channel:  "release-coordination",
		IssueKey: "PROJ-123",
		Content: notify.Content{
			Text: "going out `extracker`: https://github.com/org/repo/releases/tag/v1.2.4",
		},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if updateCalled {
		t.Error("expected no chat.update for unowned message")
	}
	if deleteCalled {
		t.Error("expected no chat.delete for unowned message")
	}
	if postCount != 1 {
		t.Errorf("expected exactly one post-fresh call, got %d", postCount)
	}
	if ref.Timestamp != "9999999999.999999" {
		t.Errorf("returned Timestamp = %q, want the new post's ts", ref.Timestamp)
	}
}

// TestNotifyUpsertsOwnMessageWithoutMetadata locks the xoxc/user-token
// path: bosun's own prior message (authored by the authenticated
// identity, mentioning the issue key) has no readable metadata, yet the
// upsert finds it by author and updates it in place rather than posting a
// duplicate.
func TestNotifyUpsertsOwnMessageWithoutMetadata(t *testing.T) {
	var (
		updatedTS string
		postCount int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "USELF"})
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "bb-prs"}},
			})
		case "/conversations.history":
			// bosun's own prior post: authored by USELF, mentions the
			// issue key, but carries no bosun metadata (xoxc token).
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "Ready for Review — PROJ-123", "ts": "1111111111.111111", "user": "USELF"},
				},
			})
		case "/chat.update":
			updatedTS = r.FormValue("ts")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": updatedTS})
		case "/chat.postMessage":
			postCount++
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channel": "C123", "ts": "9999999999.999999"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	ref, err := a.Notify(context.Background(), notify.Message{
		Channel:  "bb-prs",
		IssueKey: "PROJ-123",
		Content:  notify.Content{Header: "Ready for Review", Body: "PROJ-123 details"},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if updatedTS != "1111111111.111111" {
		t.Errorf("chat.update ts = %q, want the own message's ts", updatedTS)
	}
	if postCount != 0 {
		t.Errorf("expected no fresh post (in-place update), got %d", postCount)
	}
	if ref.Timestamp != "1111111111.111111" {
		t.Errorf("returned Timestamp = %q, want the updated message's ts", ref.Timestamp)
	}
}

// TestFindThreadSeesFreshlyPostedMessage locks the Assess/Apply cache
// consistency: after Notify posts a new message, a FindThread for the
// same channel+issue must return that message from the cache rather
// than a stale "not found" — even though FindThread's own negative
// lookup was cached before the post. This is what keeps a re-run's
// plan reading "update notification" instead of "new notification".
func TestFindThreadSeesFreshlyPostedMessage(t *testing.T) {
	var historyCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "bb-prs"}},
			})
		case "/conversations.history":
			historyCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"messages": []map[string]any{},
			})
		case "/chat.postMessage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":      true,
				"channel": "C123",
				"ts":      "1234567890.123456",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))
	ctx := context.Background()

	// Assess-side lookup before anything is posted: not found (and the
	// negative result is now cached).
	ref, err := a.FindThread(ctx, "bb-prs", "PROJ-123")
	if err != nil {
		t.Fatalf("FindThread() error: %v", err)
	}
	if ref.Timestamp != "" {
		t.Fatalf("FindThread() before post = %+v, want zero ref", ref)
	}

	posted, err := a.Notify(ctx, notify.Message{
		Channel:  "bb-prs",
		IssueKey: "PROJ-123",
		Content:  notify.Content{Header: "PROJ-123: Add widget"},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}

	ref, err = a.FindThread(ctx, "bb-prs", "PROJ-123")
	if err != nil {
		t.Fatalf("FindThread() after post error: %v", err)
	}
	if ref.Timestamp != posted.Timestamp {
		t.Errorf("FindThread() after post Timestamp = %q, want %q (the posted message)",
			ref.Timestamp, posted.Timestamp)
	}
}

// TestCacheDropsNegativeThreadEntries locks the persistence policy for
// "not found" thread lookups: they are session-only. saveCache must not
// write them (each save renews the TTL, so a frequently re-run command
// would keep a stale negative alive indefinitely), and loadCache must
// skip any a prior version already persisted.
func TestCacheDropsNegativeThreadEntries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	saveCache(apiCache{
		channels: map[string]string{"bb-prs": "C123"},
		threads: map[string]notify.ThreadRef{
			"C123:PROJ-1": {},                                                // negative: must not persist
			"C123:PROJ-2": {Channel: "C123", Timestamp: "1234567890.123456"}, // positive: must persist
		},
	})

	c := loadCache()
	if _, ok := c.threads["C123:PROJ-1"]; ok {
		t.Error("negative thread entry was persisted; want session-only")
	}
	if ref := c.threads["C123:PROJ-2"]; ref.Timestamp != "1234567890.123456" {
		t.Errorf("positive thread entry = %+v, want it persisted intact", ref)
	}
	if c.channels["bb-prs"] != "C123" {
		t.Errorf("channel entry = %q, want %q", c.channels["bb-prs"], "C123")
	}

	// A negative persisted by a prior version is pruned by the next save.
	path := cachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading cache file: %v", err)
	}
	var pc persistentCache
	if err := json.Unmarshal(data, &pc); err != nil {
		t.Fatalf("unmarshaling cache file: %v", err)
	}
	pc.Threads["C123:PROJ-3"] = cacheEntry[notify.ThreadRef]{
		Expires: time.Now().Add(time.Hour),
	}
	data, _ = json.Marshal(pc)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing cache file: %v", err)
	}

	saveCache(apiCache{})
	c = loadCache()
	if _, ok := c.threads["C123:PROJ-3"]; ok {
		t.Error("stale persisted negative survived a save; want it pruned")
	}
}

// TestNotifyIgnoresForeignMetadataMessages locks the authorship gate on
// the METADATA pass: a message carrying bosun metadata for the same
// issue key but authored by a different principal (a teammate's bosun)
// must never be updated or deleted — with a workspace-admin user token
// the delete would actually succeed and destroy their announcement.
func TestNotifyIgnoresForeignMetadataMessages(t *testing.T) {
	var updateCalled, deleteCalled bool
	var postCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "user_id": "U-SELF",
			})
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{
						"text": "going out `extracker`: v1.2.3 PROJ-123",
						"ts":   "1111111111.111111",
						"user": "U-OTHER", // someone else's bosun
						"metadata": map[string]any{
							"event_type":    "bosun_notification",
							"event_payload": map[string]any{"issue_key": "PROJ-123"},
						},
					},
				},
			})
		case "/chat.update":
			updateCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/chat.delete":
			deleteCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/chat.postMessage":
			postCount++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "channel": "C123", "ts": "9999999999.999999",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	_, err := a.Notify(context.Background(), notify.Message{
		Channel:  "release-coordination",
		IssueKey: "PROJ-123",
		Content:  notify.Content{Text: "going out `extracker`: v1.2.4 PROJ-123"},
	})
	if err != nil {
		t.Fatalf("Notify() error: %v", err)
	}
	if updateCalled {
		t.Error("chat.update was called on a foreign message")
	}
	if deleteCalled {
		t.Error("chat.delete was called on a foreign message")
	}
	if postCount != 1 {
		t.Errorf("post count = %d, want 1 fresh post", postCount)
	}
}

// TestNotifyUpsertKeepsOldMessageWhenPostFails locks the post-then-
// delete ordering of the update fallback: when the fresh post fails,
// the superseded announcement must still be standing (regression: the
// old order deleted first, so a failed post left nothing in place).
func TestNotifyUpsertKeepsOldMessageWhenPostFails(t *testing.T) {
	var deleteCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "user_id": "U-SELF",
			})
		case "/conversations.history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{
						"text": "going out `extracker`: v1.2.3 PROJ-123",
						"ts":   "1111111111.111111",
						"user": "U-SELF",
						"metadata": map[string]any{
							"event_type":    "bosun_notification",
							"event_payload": map[string]any{"issue_key": "PROJ-123"},
						},
					},
				},
			})
		case "/chat.update":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "internal_error"})
		case "/chat.delete":
			deleteCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/chat.postMessage":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "restricted_action"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	_, err := a.Notify(context.Background(), notify.Message{
		Channel:  "release-coordination",
		IssueKey: "PROJ-123",
		Content:  notify.Content{Text: "going out `extracker`: v1.2.4 PROJ-123"},
	})
	if err == nil {
		t.Fatal("expected the failed post to surface as an error")
	}
	if deleteCalled {
		t.Error("chat.delete ran before the replacement post succeeded — old announcement destroyed")
	}
}

// TestHasAnnouncementRecognizesOwnByAuthor locks the xoxc-token dedup
// path: user tokens can't read message metadata back, so the exclusion
// must also recognize the caller's own prior announcement by
// author + mention — otherwise a re-run reads it as a foreign match
// and skips the upsert, leaving the stale announcement in place.
func TestHasAnnouncementRecognizesOwnByAuthor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "releases"}},
			})
		case "/auth.test":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-SELF"})
		case "/conversations.history":
			// Our own prior announcement — no readable metadata (xoxc),
			// attributed by author, mentioning the issue key.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{
						"text": "going out `api` v1.2.3 (PROJ-9)",
						"ts":   "1111111111.111111",
						"user": "U-SELF",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := NewWithOptions("test-token", slackapi.OptionAPIURL(server.URL+"/"))

	got, err := a.HasAnnouncement(context.Background(), "releases", "v1.2.3", "PROJ-9")
	if err != nil {
		t.Fatalf("HasAnnouncement() error: %v", err)
	}
	if got {
		t.Error("own author-matched announcement counted as a foreign match")
	}
}

// TestSlackIconURL confirms a Jira issue-type icon URL is normalized for
// Slack's image proxy: universal_avatar URLs (SVG by default) get
// format=png plus the shared card size; legacy SVG system icons are
// swapped for the PNG sibling Jira serves; empty/unusable inputs return
// "" so the card falls back to the :jira: glyph.
func TestSlackIconURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{
			"universal_avatar forces png and size",
			"https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?size=medium",
			"https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?format=png&size=" + cardIconJiraSize,
		},
		{
			"legacy svg system icon swapped to png",
			"https://x.atlassian.net/images/icons/issuetypes/epic.svg",
			"https://x.atlassian.net/images/icons/issuetypes/epic.png",
		},
		{
			"plain raster passes through",
			"https://x.atlassian.net/images/icons/issuetypes/story.png",
			"https://x.atlassian.net/images/icons/issuetypes/story.png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slackIconURL(tc.in); got != tc.want {
				t.Errorf("slackIconURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderContentJiraCard confirms the Jira ticket card: title formatted
// as "[KEY] Title", a primary "View Issue" button, the normalized icon,
// no :jira: glyph when a real icon is present, and the placeholder body.
func TestRenderContentJiraCard(t *testing.T) {
	rawIcon := "https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?size=medium"
	sections := renderContent(notify.Content{
		Issue: &notify.IssueRef{
			Key: "PROJ-1", Title: "Add widget", Type: "Story",
			URL: "https://jira.example.com/browse/PROJ-1", IconURL: rawIcon,
		},
	})
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	card := sections[0]
	if card.Text != "[PROJ-1] Add widget" {
		t.Errorf("Text = %q, want %q", card.Text, "[PROJ-1] Add widget")
	}
	if strings.Contains(card.Text, glyphJira) {
		t.Errorf("Text = %q, want no glyph when an icon is present", card.Text)
	}
	wantIcon := "https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?format=png&size=" + cardIconJiraSize
	if card.IconURL != wantIcon {
		t.Errorf("IconURL = %q, want %q", card.IconURL, wantIcon)
	}
	if len(card.Buttons) != 1 || card.Buttons[0].Text != btnViewIssue || card.Buttons[0].Style != "primary" {
		t.Errorf("Buttons = %+v, want one primary %q", card.Buttons, btnViewIssue)
	}
	if card.Body != noDescription {
		t.Errorf("Body = %q, want placeholder %q", card.Body, noDescription)
	}
}

// TestRenderContentJiraGlyphFallback confirms the :jira: glyph prefixes the
// title when there's no usable icon.
func TestRenderContentJiraGlyphFallback(t *testing.T) {
	sections := renderContent(notify.Content{
		Issue: &notify.IssueRef{Key: "PROJ-1", Title: "Add widget"},
	})
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	want := glyphJira + " [PROJ-1] Add widget"
	if sections[0].Text != want {
		t.Errorf("Text = %q, want %q", sections[0].Text, want)
	}
	if sections[0].IconURL != "" {
		t.Errorf("IconURL = %q, want empty", sections[0].IconURL)
	}
}

// TestRenderContentItemAndPreviewCards confirms the ephemeral preview card
// ("View Deployment") and per-repo PR item cards (primary "View Pull
// Request" + "View Branch"), carrying the raw GitHub avatar as the icon.
func TestRenderContentItemAndPreviewCards(t *testing.T) {
	avatar := "https://github.com/octocat.png?size=48"
	sections := renderContent(notify.Content{
		Issue:   &notify.IssueRef{Key: "PROJ-1", Title: "Add widget"},
		IconURL: avatar,
		Preview: &notify.PreviewRef{Name: "brave-falcon", URL: "https://preview.example.com"},
		Items: []notify.Item{
			{
				Label: "my-service", URL: "https://github.com/org/my-service/pull/42",
				Detail: "#42", Body: "PR body",
				BranchURL: "https://github.com/org/my-service/tree/feat",
			},
		},
	})
	// Jira card, preview card, then one item card.
	if len(sections) != 3 {
		t.Fatalf("got %d sections, want 3", len(sections))
	}

	preview := sections[1]
	if preview.Text != glyphCloud+" brave-falcon" {
		t.Errorf("preview Text = %q, want %q", preview.Text, glyphCloud+" brave-falcon")
	}
	if len(preview.Buttons) != 1 || preview.Buttons[0].Text != btnViewDeployment {
		t.Errorf("preview Buttons = %+v, want one %q", preview.Buttons, btnViewDeployment)
	}

	item := sections[2]
	if item.Text != "[PROJ-1] Add widget" {
		t.Errorf("item Text = %q", item.Text)
	}
	if item.Subtitle != "`my-service` #42" {
		t.Errorf("item Subtitle = %q, want %q", item.Subtitle, "`my-service` #42")
	}
	if item.IconURL != avatar {
		t.Errorf("item IconURL = %q, want the raw GitHub avatar %q", item.IconURL, avatar)
	}
	if len(item.Buttons) != 2 ||
		item.Buttons[0].Text != btnViewPullRequest || item.Buttons[0].Style != "primary" ||
		item.Buttons[1].Text != btnViewBranch {
		t.Errorf("item Buttons = %+v, want primary %q + %q", item.Buttons, btnViewPullRequest, btnViewBranch)
	}
	if item.Body != "PR body" {
		t.Errorf("item Body = %q, want %q", item.Body, "PR body")
	}
}
