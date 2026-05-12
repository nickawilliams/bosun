package preview

import (
	"errors"
	"strings"
	"testing"
)

func TestProbeError(t *testing.T) {
	inner := errors.New("connection refused")
	pe := &ProbeError{URL: "https://example.com/preview", Err: inner}

	if got, want := pe.Error(), "probing https://example.com/preview: connection refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(pe, inner) {
		t.Error("errors.Is should unwrap to the inner error")
	}
	var unwrapped *ProbeError
	if !errors.As(pe, &unwrapped) {
		t.Error("errors.As should match ProbeError")
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"brave-falcon", false},
		{"a", false},
		{"a1", false},
		{"team-1-preview-x", false},
		{"", true},
		{"BraveFalcon", true},        // uppercase
		{"-leading-dash", true},      // leading hyphen
		{"trailing-dash-", true},     // trailing hyphen
		{"1starts-with-digit", true}, // digit start
		{"has_underscore", true},     // underscore
		{"has spaces", true},         // space
		{"has.dot", true},            // dot
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateName(%q) error = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
		})
	}

	// Length boundaries.
	t.Run("63 chars", func(t *testing.T) {
		s := "a" + strings.Repeat("b", 62)
		if err := ValidateName(s); err != nil {
			t.Fatalf("63-char name should be valid: %v", err)
		}
	})
	t.Run("64 chars", func(t *testing.T) {
		s := "a" + strings.Repeat("b", 63)
		if err := ValidateName(s); err == nil {
			t.Fatalf("64-char name should be invalid")
		}
	})
}
