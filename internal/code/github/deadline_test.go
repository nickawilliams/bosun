package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/code"
)

// Deadline contract for the code host, mirroring the Jira adapter's.
// Issue #54 asked for the sibling adapters to be audited rather than
// only the tracker fixed — these are that audit, kept executable.
//
// This adapter carries a wrinkle the tracker does not: several read
// paths follow Link-header pagination and issue N sequential requests
// under one caller deadline. Those are bounded by the caller's
// context rather than by any per-request budget, so the deadline
// tests here cover a paginating method as well as a single-shot one.

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

func TestGetAuthenticatedUserHonorsContextDeadline(t *testing.T) {
	server, requests := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		_, err := adapter.GetAuthenticatedUser(ctx)
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("issued %d requests, want 1 — the adapter must not retry", got)
	}
}

// ListBranches pages until the Link header runs out. A server that
// never answers must stop it at the caller's deadline rather than at
// the end of the pagination it never reaches.
func TestListBranchesHonorsContextDeadline(t *testing.T) {
	server, _ := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		_, err := adapter.ListBranches(ctx, "owner", "repo")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestCreatePRHonorsContextDeadline(t *testing.T) {
	server, _ := hangingServer(t)
	adapter := NewWithClient(server.Client(), server.URL, "token")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := mustReturnWithin(t, 5*time.Second, func() error {
		_, err := adapter.CreatePR(ctx, code.CreatePRRequest{
			Owner: "owner", Repository: "repo", Head: "feature", Base: "main", Title: "t",
		})
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

func TestNewSetsRequestTimeout(t *testing.T) {
	adapter := New("token")

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
// context with no deadline at all.
func TestRequestTimeoutBoundsDeadlinelessCaller(t *testing.T) {
	server, _ := hangingServer(t)

	// The backstop is baked into defaultClient at init, so the knob
	// that matters is the client's own field. Overriding it here keeps
	// the test on the exact client New installs rather than a stand-in.
	orig := defaultClient.Timeout
	defaultClient.Timeout = 150 * time.Millisecond
	t.Cleanup(func() { defaultClient.Timeout = orig })

	adapter := New("token")
	adapter.baseURL = server.URL

	err := mustReturnWithin(t, 5*time.Second, func() error {
		//nolint:usetesting // context.Background is the condition under test.
		_, err := adapter.GetAuthenticatedUser(context.Background())
		return err
	})
	if err == nil {
		t.Fatal("GetAuthenticatedUser with a deadline-free context returned nil against a hanging server")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}
