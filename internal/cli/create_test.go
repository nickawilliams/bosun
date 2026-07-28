package cli

import "testing"

// TestFirstLineSummary locks the simplified representation record cards
// use for textarea input: first non-empty line, ellipsis when content
// was elided.
func TestFirstLineSummary(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single line untouched", "fix the widget", "fix the widget"},
		{"multi-line collapses with ellipsis", "first line\nsecond line", "first line …"},
		{"trailing newline only — no ellipsis", "just one line\n", "just one line"},
		{"trailing blank lines — no ellipsis", "line\n\n  \n", "line"},
		{"leading whitespace trimmed", "  padded\nmore", "padded …"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstLineSummary(tt.in); got != tt.want {
				t.Errorf("firstLineSummary(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
