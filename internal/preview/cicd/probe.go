package cicd

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"time"
)

// classifyProbeStatus maps an HTTP status code to (alive, definitive). 5xx is
// indefinite (caller should retry); 404 is a definitive miss; anything else
// 2xx-4xx is alive (auth-gated envs return 401/403 and we treat that as a
// signal that the host is reachable).
func classifyProbeStatus(status int) (alive, definitive bool) {
	switch {
	case status == http.StatusNotFound:
		return false, true
	case status >= 500 && status < 600:
		return false, false
	case status >= 200 && status < 500:
		return true, true
	default:
		return false, false
	}
}

// probeClient is shared across every probe. http.Transport is designed to be
// long-lived and pools connections, so building one per call both stranded its
// idle connections (nothing closed them) and defeated reuse across the repeated
// probes a multi-repo fan-out issues.
//
// InsecureSkipVerify is deliberate: preview environments routinely run behind
// self-signed or short-lived certs, and the probe only classifies reachability.
// Per-attempt deadlines come from the request context, not the client.
var probeClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

// httpProbe sends a HEAD (falling back to GET on 405) and classifies the
// response. One retry on transient errors. Returns (alive, nil) on a
// definitive 2xx-4xx, (false, nil) on a definitive 404, and an error
// when no attempt produced a definitive result.
func httpProbe(ctx context.Context, url string) (bool, error) {
	do := func(method string) (int, error) {
		rc, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rc, method, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := probeClient.Do(req)
		if err != nil {
			return 0, err
		}
		// Drain before closing: closing a partially-read body strands the
		// connection instead of returning it to the pool, which would
		// defeat the shared transport above. HEAD has no body, so this is
		// a no-op on the common path.
		//
		// Deliberately unbounded. A byte cap here would silently reinstate
		// the very problem this avoids for any page larger than the cap —
		// and a preview environment's landing page easily clears any cap
		// worth writing down. The read is not open-ended: rc still governs
		// the body, so a slow or endless response is cut off by the same
		// 3s deadline as the request, and io.Discard accumulates nothing.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	attempt := func() (alive, definitive bool, err error) {
		status, err := do(http.MethodHead)
		if err != nil {
			return false, false, err
		}
		if status == http.StatusMethodNotAllowed {
			status, err = do(http.MethodGet)
			if err != nil {
				return false, false, err
			}
		}
		alive, definitive = classifyProbeStatus(status)
		return alive, definitive, nil
	}

	var lastErr error
	for range 2 {
		alive, definitive, err := attempt()
		if err == nil && definitive {
			return alive, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("indeterminate response after retry")
	}
	return false, lastErr
}
