package slack

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// cookieTransport is what makes xoxc- token auth work at all — Slack
// rejects those tokens without the paired d cookie — and it had no
// test. The header is written into the map directly rather than
// through Header.Set, so the escape hatch for non-ASCII cookie bytes
// is part of the contract worth pinning.

func TestCookieTransportAttachesCookie(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := &http.Client{Transport: &cookieTransport{
		base:   http.DefaultTransport,
		cookie: "xoxd-secret",
	}}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if want := "d=xoxd-secret"; got != want {
		t.Errorf("Cookie header = %q, want %q", got, want)
	}
}

// Header.Set would reject these bytes; the transport writes the map
// entry directly so a raw cookie lifted from the desktop app survives.
func TestCookieTransportAllowsNonASCIICookie(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := &http.Client{Transport: &cookieTransport{
		base:   http.DefaultTransport,
		cookie: "xoxd-café",
	}}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get with a non-ASCII cookie: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if want := "d=xoxd-café"; got != want {
		t.Errorf("Cookie header = %q, want %q", got, want)
	}
}

// The transport must not mutate the caller's request — it clones
// before writing the header.
func TestCookieTransportDoesNotMutateCallerRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	transport := &cookieTransport{base: http.DefaultTransport, cookie: "xoxd-secret"}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, ok := req.Header["Cookie"]; ok {
		t.Error("RoundTrip wrote the Cookie header onto the caller's request instead of a clone")
	}
}
