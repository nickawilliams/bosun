package githubactions

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/cicd"
)

// Deadline contract for the CI/CD adapter, completing the sibling
// audit issue #54 asked for. This is the adapter `bosun doctor`
// reaches on its CI/CD check, and the one a pipeline-gating run is
// most likely to have pointed at an unreachable host.

func hangingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var requests atomic.Int64
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
	}))

	// LIFO: close(release) must run before Close, which blocks until
	// every outstanding handler returns.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	return server, &requests
}

func TestTriggerWorkflowHonorsContextDeadline(t *testing.T) {
	server, requests := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- adapter.TriggerWorkflow(ctx, cicd.TriggerRequest{
			Owner: "owner", Repository: "repo", Workflow: "deploy.yml", Ref: "main",
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TriggerWorkflow did not return within 5s; the context deadline was not honored")
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("issued %d requests, want 1 — the adapter must not retry", got)
	}
}

func TestNewSetsRequestTimeout(t *testing.T) {
	adapter := New("token")

	if adapter.client.Timeout == 0 {
		t.Fatal("New returned an adapter whose client has no timeout")
	}
	if adapter.client.Timeout != requestTimeout {
		t.Errorf("client.Timeout = %v, want %v", adapter.client.Timeout, requestTimeout)
	}
	if adapter.client == http.DefaultClient {
		t.Error("adapter uses http.DefaultClient, which is shared process-wide and has no timeout")
	}
}
