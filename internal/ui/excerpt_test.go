package ui

import "testing"

// TestExcerpt locks the bounded multi-line record convention: first max
// lines verbatim, elided content collapsed into a "… +K lines" marker,
// surrounding whitespace trimmed so trailing newlines don't count.
func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"single line untouched", "fix the widget", 3, "fix the widget"},
		{"at the limit — no marker", "a\nb\nc", 3, "a\nb\nc"},
		{"one over — singular marker", "a\nb\nc\nd", 3, "a\nb\nc\n… +1 line"},
		{"several over — plural marker", "a\nb\nc\nd\ne\nf", 3, "a\nb\nc\n… +3 lines"},
		{"trailing newline not counted", "a\nb\nc\n", 3, "a\nb\nc"},
		{"internal blank lines count", "a\n\nb\n\nc", 3, "a\n\nb\n… +2 lines"},
		{"empty", "", 3, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Excerpt(tt.in, tt.max); got != tt.want {
				t.Errorf("Excerpt(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}
