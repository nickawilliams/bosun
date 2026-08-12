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
