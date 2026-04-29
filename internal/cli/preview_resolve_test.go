package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidatePreviewName(t *testing.T) {
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
			err := validatePreviewName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePreviewName(%q) error = %v, wantErr %v", tc.name, err, tc.wantErr)
			}
		})
	}

	// Length boundaries.
	t.Run("63 chars", func(t *testing.T) {
		s := "a" + strings.Repeat("b", 62)
		if err := validatePreviewName(s); err != nil {
			t.Fatalf("63-char name should be valid: %v", err)
		}
	})
	t.Run("64 chars", func(t *testing.T) {
		s := "a" + strings.Repeat("b", 63)
		if err := validatePreviewName(s); err == nil {
			t.Fatalf("64-char name should be invalid")
		}
	})
}

func TestClassifyProbeStatus(t *testing.T) {
	cases := []struct {
		status         int
		wantAlive      bool
		wantDefinitive bool
	}{
		{http.StatusOK, true, true},
		{http.StatusNoContent, true, true},
		{http.StatusMovedPermanently, true, true},
		{http.StatusUnauthorized, true, true},   // auth-gated env, host responds
		{http.StatusForbidden, true, true},      // ditto
		{http.StatusNotFound, false, true},      // env doesn't exist
		{http.StatusBadGateway, false, false},   // ambiguous, retry
		{http.StatusServiceUnavailable, false, false},
		{http.StatusGatewayTimeout, false, false},
		{0, false, false},
	}
	for _, tc := range cases {
		alive, definitive := classifyProbeStatus(tc.status)
		if alive != tc.wantAlive || definitive != tc.wantDefinitive {
			t.Errorf("classifyProbeStatus(%d) = (%v, %v), want (%v, %v)",
				tc.status, alive, definitive, tc.wantAlive, tc.wantDefinitive)
		}
	}
}
