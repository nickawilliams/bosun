package cicd

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHttpProbeAlive(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"200 OK", http.StatusOK},
		{"204 No Content", http.StatusNoContent},
		{"301 Redirect", http.StatusMovedPermanently},
		{"401 Unauthorized (auth-gated)", http.StatusUnauthorized},
		{"403 Forbidden (auth-gated)", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			alive, err := httpProbe(context.Background(), server.URL)
			if err != nil {
				t.Fatalf("httpProbe error: %v", err)
			}
			if !alive {
				t.Errorf("expected alive=true for status %d", tc.status)
			}
		})
	}
}

func TestHttpProbeDead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	alive, err := httpProbe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("httpProbe error: %v", err)
	}
	if alive {
		t.Error("expected alive=false for 404")
	}
}

func TestHttpProbeIndeterminate(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := httpProbe(context.Background(), server.URL)
	if err == nil {
		t.Fatal("expected error for repeated 5xx")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 retries, got %d calls", got)
	}
}

func TestHttpProbeHEADFallbackToGET(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	alive, err := httpProbe(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("httpProbe error: %v", err)
	}
	if !alive {
		t.Error("expected alive after GET fallback")
	}
	if len(methods) != 2 || methods[0] != http.MethodHead || methods[1] != http.MethodGet {
		t.Errorf("expected [HEAD, GET], got %v", methods)
	}
}

// A transport built per call opens a fresh connection every probe and strands
// the previous one; so does closing a response body without draining it. Both
// regressions surface the same way — the server sees a new connection per probe
// instead of one reused across all of them.
func TestHttpProbeReusesConnections(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "HEAD path",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
		},
		{
			// The GET fallback carries a body, so this case is the one
			// that fails if the body is closed without being drained.
			name: "GET fallback with body",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
			},
		},
		{
			// A real preview landing page is far larger than any byte cap
			// worth writing down. Draining only the first N bytes would
			// strand the connection here while the small case above kept
			// passing, so the large case is the one that pins the property.
			name: "GET fallback with a body larger than any drain cap",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 512<<10))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var newConns atomic.Int32
			// Unstarted so ConnState is installed before the serve
			// goroutine reads it.
			server := httptest.NewUnstartedServer(tc.handler)
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					newConns.Add(1)
				}
			}
			server.Start()
			defer server.Close()

			for i := range 3 {
				if _, err := httpProbe(context.Background(), server.URL); err != nil {
					t.Fatalf("probe %d: %v", i, err)
				}
			}

			if got := newConns.Load(); got != 1 {
				t.Errorf("expected 1 connection reused across 3 probes, got %d", got)
			}
		})
	}
}

func TestHttpProbeNetworkError(t *testing.T) {
	// Start then immediately close — probing the URL produces a
	// connection-refused error.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := httpProbe(context.Background(), url)
	if err == nil {
		t.Fatal("expected error for closed server")
	}
}
