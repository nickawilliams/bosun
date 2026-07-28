package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestCardKVWrapsWithHangingIndent locks the KV value-wrapping
// behavior: a value longer than the width left of the value column
// wraps inside the KV renderer, with every fragment aligned under the
// value column (hanging indent) — not re-broken at the content margin
// by the generic wrapForTimeline pass.
func TestCardKVWrapsWithHangingIndent(t *testing.T) {
	// Tests run with a non-file output stream → TermWidth() == 80.
	long := strings.Repeat("lorem ipsum dolor sit amet ", 8) // ~216 cells, forces wrapping
	rendered := ansi.Strip(NewCard(CardSuccess, "record").
		KV("Description", long, "Type", "Story").Render())

	lines := strings.Split(rendered, "\n")

	// Find the Description row and its continuations.
	var descIdx int
	for i, l := range lines {
		if strings.Contains(l, "Description") {
			descIdx = i
			break
		}
	}
	if descIdx == 0 && !strings.Contains(lines[0], "Description") {
		t.Fatalf("no Description row in:\n%s", rendered)
	}

	// prefixWidth = maxKey("Description"=11) + 2 = 13; continuation
	// lines carry that padding plus the joining space before content.
	contPrefix := strings.Repeat(" ", 13) + " "
	var continuations int
	for _, l := range lines[descIdx+1:] {
		if strings.Contains(l, "Type") {
			break
		}
		body := strings.TrimPrefix(l, " │  ") // timeline connector prefix
		if !strings.HasPrefix(body, contPrefix) {
			t.Errorf("continuation not aligned under value column: %q", l)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("blank continuation line: %q", l)
		}
		continuations++
	}
	if continuations < 2 {
		t.Errorf("expected the long value to wrap into multiple continuations, got %d\n%s", continuations, rendered)
	}

	// Every rendered line must fit the 80-col budget minus the
	// connector — proving the generic wrap pass won't re-break them.
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line exceeds terminal width (%d): %q", w, l)
		}
	}
}
