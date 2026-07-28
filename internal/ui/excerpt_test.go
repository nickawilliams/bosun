package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestExcerpt locks the bounded multi-line record convention: first max
// lines verbatim, elided content collapsed into a "… +K lines" marker,
// surrounding whitespace trimmed so trailing newlines don't count.
// Content assertions strip ANSI because the marker line is pre-styled
// muted (meta-text about the value, not part of it) — content lines
// must pass through unstyled for the surrounding record to style.
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
			if got := ansi.Strip(Excerpt(tt.in, tt.max)); got != tt.want {
				t.Errorf("Excerpt(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}

	// Content lines must pass through byte-identical (no styling) —
	// only the marker line may carry ANSI.
	out := Excerpt("a\nb\nc\nd", 3)
	lines := strings.Split(out, "\n")
	for i, l := range lines[:3] {
		if l != []string{"a", "b", "c"}[i] {
			t.Errorf("content line %d altered: %q", i, l)
		}
	}
}
