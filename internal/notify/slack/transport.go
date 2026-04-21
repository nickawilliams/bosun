package slack

import "net/http"

// cookieTransport wraps an http.RoundTripper to inject the Slack d cookie
// into every request. This is required when using xoxc- tokens extracted
// from the Slack desktop app.
type cookieTransport struct {
	base   http.RoundTripper
	cookie string
}

func (t *cookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	// Write to the header map directly to bypass Header.Set's validation,
	// which rejects non-ASCII bytes that may be present in the raw cookie.
	req.Header["Cookie"] = []string{"d=" + t.cookie}
	return t.base.RoundTrip(req)
}
