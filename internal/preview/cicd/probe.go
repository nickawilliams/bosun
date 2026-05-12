package cicd

import (
	"context"
	"crypto/tls"
	"errors"
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

// httpProbe sends a HEAD (falling back to GET on 405) and classifies the
// response. One retry on transient errors. Returns (alive, nil) on a
// definitive 2xx-4xx, (false, nil) on a definitive 404, and an error
// when no attempt produced a definitive result.
func httpProbe(ctx context.Context, url string) (bool, error) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	do := func(method string) (int, error) {
		rc, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rc, method, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
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
