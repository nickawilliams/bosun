package cicd

import (
	"context"
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
