package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		case "/conversations.history":
			// Existing message keyed on bosun metadata for PROJ-123.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{
						"text": "going out `extracker`: https://github.com/org/repo/releases/tag/v1.2.3",
						"ts":   "1111111111.111111",
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

// TestNotifyIgnoresUnownedMessages locks the metadata-only upsert
// match. A message in the channel that mentions the issue key in its
// text but has no bosun metadata is NOT touched — we don't risk a
// cant_update_message / cant_delete_message on a stale text match.
// Notify posts fresh; the unrelated message stays untouched.
func TestNotifyIgnoresUnownedMessages(t *testing.T) {
	var (
		updateCalled bool
		deleteCalled bool
		postCount    int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/conversations.list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":       true,
				"channels": []map[string]any{{"id": "C123", "name": "release-coordination"}},
			})
		case "/conversations.history":
			// A message mentioning the issue key but WITHOUT bosun
			// metadata — could be a manual post, a reply, or another
			// tool. The upsert path must not touch it.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"text": "anything about PROJ-123 here", "ts": "1111111111.111111"},
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
