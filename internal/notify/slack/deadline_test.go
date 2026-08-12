package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
)

// The fourth adapter in the #54 audit. slack-go's default client sets
// no timeout, and this adapter is reached from doctor, review,
// prerelease, and release — every one of which hands it the command
// context, which carries no deadline of its own. Without a backstop an
// unresponsive Slack host hangs those commands with nothing to cancel
// them.

func TestNewHTTPClientSetsRequestTimeout(t *testing.T) {
	client := newHTTPClient()

	if client == http.DefaultClient {
		t.Fatal("adapter uses http.DefaultClient, which is shared process-wide and has no timeout")
	}
	if client.Timeout != requestTimeout {
		t.Errorf("client.Timeout = %v, want %v", client.Timeout, requestTimeout)
	}
	// Transport stays nil so the client still uses http.DefaultTransport,
	// keeping connection pooling identical to slack-go's own default.
	if client.Transport != nil {
		t.Errorf("Transport = %v, want nil so http.DefaultTransport is used", client.Transport)
	}
}

// The backstop has to actually bound a call, not just populate a
// field: a caller with no deadline against a host that never answers.
func TestRequestTimeoutBoundsDeadlinelessCaller(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	// LIFO: close(release) must run before Close, which blocks until
	// every outstanding handler returns.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	client := newHTTPClient()
	client.Timeout = 150 * time.Millisecond

	a := NewWithOptions("test-token",
		slackapi.OptionAPIURL(server.URL+"/"),
		slackapi.OptionHTTPClient(client),
	)

	done := make(chan error, 1)
	go func() {
		//nolint:usetesting // context.Background is the condition under test.
		_, err := a.AuthTest(context.Background())
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("AuthTest with a deadline-free context returned nil against a hanging server")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AuthTest did not return within 5s; the request timeout did not bound a deadline-free caller")
	}
}

// The cookie constructor carries the same backstop. It builds its own
// client rather than reusing newHTTPClient because it needs the cookie
// transport, which is exactly how a second constructor drifts out of
// sync with the first.
func TestNewCookieClientSetsRequestTimeout(t *testing.T) {
	client := newCookieClient("xoxd-secret")

	if client.Timeout != requestTimeout {
		t.Errorf("client.Timeout = %v, want %v", client.Timeout, requestTimeout)
	}

	ct, ok := client.Transport.(*cookieTransport)
	if !ok {
		t.Fatalf("Transport = %T, want *cookieTransport", client.Transport)
	}
	if ct.cookie != "xoxd-secret" {
		t.Errorf("cookie = %q, want %q", ct.cookie, "xoxd-secret")
	}
	// Layered over DefaultTransport so pooling matches the token path.
	if ct.base != http.DefaultTransport {
		t.Error("cookie transport does not wrap http.DefaultTransport")
	}
}

// Both production constructors must return an adapter whose cache maps
// are initialized — Notify writes into them, and a nil map panics.
// NewWithOptions is the test-only seam and does not seed the cache, so
// these are the only two paths that guarantee it.
func TestProductionConstructorsInitializeCache(t *testing.T) {
	tests := []struct {
		name    string
		adapter *Adapter
	}{
		{"New", New("xoxb-token")},
		{"NewWithCookie", NewWithCookie("xoxc-token", "xoxd-secret")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.adapter == nil {
				t.Fatal("constructor returned nil")
			}
			if tt.adapter.client == nil {
				t.Error("adapter has no slack client")
			}
			if tt.adapter.cache.channels == nil {
				t.Error("cache.channels is nil; a channel-ID write would panic")
			}
			if tt.adapter.cache.threads == nil {
				t.Error("cache.threads is nil; a thread write would panic")
			}
		})
	}
}
