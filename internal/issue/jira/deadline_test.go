package jira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/issue"
)

// The caller's deadline is the adapter's contract: a command that
// wraps a tracker call in a timeout must get control back when that
// timeout fires, however unresponsive the host is. These pin that,
// because the failure it guards against is invisible from a green
// test suite — every test hits a server that answers promptly, so an
// adapter could stop honoring ctx entirely without one going red.
//
// See issue #54, which observed a `bosun doctor` run overrunning its
// 30s check budget and attributed it to a retry loop in this adapter.
// There is no retry loop here, and these tests exist partly to keep
// that answer verifiable rather than a claim in a commit message.

// hangingServer returns a server whose handler blocks until the test
// ends, simulating a host that accepts a connection and then never
// answers. Requests are counted so a caller can also assert that a
// method issues exactly one request rather than retrying.
//
// Cleanup order matters and is the reason the registration looks
// backwards: t.Cleanup runs LIFO, and httptest's Close blocks until
// every outstanding handler returns. Registering Close first means
// the release channel closes first, unblocking the handler so Close
// can finish — the other order deadlocks the test binary.
func hangingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var requests atomic.Int64
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
	}))

	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	return server, &requests
}

// mustReturnWithin runs call and fails if it does not return inside
// limit. An adapter that stops honoring its context blocks forever
// against hangingServer, so asserting inline would hang the package
// until the go test timeout kills it and reports a stack dump rather
// than a failure. Running the call off the test goroutine turns that
// into a named failure on the test that regressed.
//
// The call is left running on regression — it is blocked on a handler
// that only the cleanup releases, so it cannot be joined here.
func mustReturnWithin(t *testing.T, limit time.Duration, call func() error) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- call() }()

	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("call did not return within %v; the context deadline was not honored", limit)
		return nil
	}
}

func TestGetIssueHonorsContextDeadline(t *testing.T) {
	server, requests := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "user@example.com", "token")

	const budget = 150 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	start := time.Now()
	err := mustReturnWithin(t, budget*10, func() error {
		_, err := adapter.GetIssue(ctx, "PROJ-1")
		return err
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("GetIssue against a hanging server returned nil error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	// mustReturnWithin already bounds this; logging the real figure
	// makes a near-miss visible before it starts flaking.
	t.Logf("GetIssue returned in %v with a %v deadline", elapsed, budget)
	// Retrying past an expired deadline is the specific shape issue
	// #54 suspected. One request in, one request out.
	// Asserts "never more than one", not "exactly one": the handler
	// goroutine may not be scheduled before a deadline this short, and
	// the guarantee under test is the absence of a retry.
	if got := requests.Load(); got > 1 {
		t.Errorf("issued %d requests, want at most 1 — the adapter must not retry", got)
	}
}

// An already-expired context must fail before the adapter reaches the
// network at all. A retry loop that checked the deadline only between
// attempts would still issue this first request.
func TestGetIssueRejectsExpiredContext(t *testing.T) {
	server, requests := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "user@example.com", "token")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		_, err := adapter.GetIssue(ctx, "PROJ-1")
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("issued %d requests with an expired context, want 0", got)
	}
}

// The write paths take the same doRequest seam, but they carry a body
// — asserting one of them keeps a future refactor from bypassing the
// context on the paths that mutate state.
func TestSetStatusHonorsContextDeadline(t *testing.T) {
	server, _ := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "user@example.com", "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		return adapter.SetStatus(ctx, "PROJ-1", "Done")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestListIssuesHonorsContextDeadline(t *testing.T) {
	server, _ := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "user@example.com", "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		_, err := adapter.ListIssues(ctx, issue.ListQuery{Project: "PROJ"})
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// New must not hand back a client that can block forever. Callers
// that pass a context with no deadline exist (the issue picker among
// them), and http.DefaultClient — the previous default — has no
// timeout, so for those callers an unreachable host meant an
// unbounded hang with nothing to cancel it.
func TestNewSetsRequestTimeout(t *testing.T) {
	adapter := New("https://jira.example.com", "user@example.com", "token")

	// Identity first: http.DefaultClient's Timeout is zero, so a
	// Timeout assertion alone would fire before ever reaching this and
	// leave the real regression — silently sharing the process-wide
	// client — untested.
	if adapter.client == http.DefaultClient {
		t.Fatal("adapter uses http.DefaultClient, which is shared process-wide and has no timeout")
	}
	if adapter.client.Timeout != requestTimeout {
		t.Errorf("client.Timeout = %v, want %v", adapter.client.Timeout, requestTimeout)
	}
}

// The backstop has to actually bound a call, not just populate a
// field. This is the path that matters for it: a caller handing over a
// context with no deadline at all, which is what the issue picker and
// the status remote parse do.
func TestRequestTimeoutBoundsDeadlinelessCaller(t *testing.T) {
	server, _ := hangingServer(t)

	// The backstop is baked into defaultClient at init, so the knob
	// that matters is the client's own field. Overriding it here keeps
	// the test on the exact client New installs rather than a stand-in.
	orig := defaultClient.Timeout
	defaultClient.Timeout = 150 * time.Millisecond
	t.Cleanup(func() { defaultClient.Timeout = orig })

	// Built through New so the test exercises the client New actually
	// installs, with only the base URL redirected at the fake host.
	adapter := New(server.URL, "user@example.com", "token")

	err := mustReturnWithin(t, 5*time.Second, func() error {
		//nolint:usetesting // context.Background is the condition under test.
		_, err := adapter.GetIssue(context.Background(), "PROJ-1")
		return err
	})
	if err == nil {
		t.Fatal("GetIssue with a deadline-free context returned nil against a hanging server")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}
