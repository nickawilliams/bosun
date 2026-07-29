package cli

import (
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

func TestTransformFieldTitle(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"sentence case normalized", "repository patterns", "Repository Patterns"},
		{"mixed case left alone for words with uppercase", "repository GitHub patterns", "Repository GitHub Patterns"},
		{"already title-cased is idempotent", "Workspace Root", "Workspace Root"},
		{"acronym preserved by titleCase", "API key", "API Key"},
		{"verbatim opt-out via ui.PreserveCase", ui.PreserveCase("API key"), "API key"},
		{"verbatim works on already-correct strings", ui.PreserveCase("GitHub"), "GitHub"},
		{"empty string stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transformFieldTitle(tc.input)
			if got != tc.want {
				t.Errorf("transformFieldTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
