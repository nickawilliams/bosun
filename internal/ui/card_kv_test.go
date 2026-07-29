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

// TestCardKVNarrowClampKeepsLinesBounded locks the degraded narrow
// layout: when the key is so wide that the full hanging indent leaves
// no readable value column (the old code clamped the wrap width and
// emitted lines wider than the timeline, which wrapForTimeline then
// re-broke at the content margin, losing the column), the key takes
// its own line and every fragment wraps at a reduced indent — with no
// emitted line exceeding the timeline's content width.
func TestCardKVNarrowClampKeepsLinesBounded(t *testing.T) {
	// TermWidth() == 80 in tests → maxContent = 75. A 60-char key
	// makes prefixWidth 62, leaving 12 (< 20) — the clamp engages.
	wideKey := strings.Repeat("k", 60)
	long := strings.Repeat("word ", 30)
	rendered := ansi.Strip(NewCard(CardSuccess, "record").
		KV(wideKey, long).Render())

	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	const maxContent = 75

	var keyLine, contLines int
	for _, l := range lines {
		body := l
		// The outer render adds the glyph/connector prefix; measure the
		// full physical line — nothing may exceed the terminal width
		// minus nothing (the whole point is no physical wrap).
		if w := len([]rune(l)); w > 80 {
			t.Errorf("line exceeds terminal width (%d): %q", w, l)
		}
		if strings.Contains(body, wideKey) {
			keyLine++
			if strings.Contains(body, "word") {
				t.Errorf("key line also carries value content: %q", l)
			}
		}
		if strings.Contains(body, "word") {
			contLines++
		}
	}
	if keyLine != 1 {
		t.Fatalf("expected the wide key on exactly one line, got %d:\n%s", keyLine, rendered)
	}
	if contLines < 2 {
		t.Fatalf("expected the value wrapped across multiple lines, got %d:\n%s", contLines, rendered)
	}

	// All value fragments share one indent column (the hanging indent
	// survives, just reduced).
	var indents []int
	for _, l := range lines {
		if strings.Contains(l, "word") {
			indents = append(indents, len(l)-len(strings.TrimLeft(l, " │")))
		}
	}
	for _, ind := range indents[1:] {
		if ind != indents[0] {
			t.Errorf("value fragments not column-aligned: indents %v", indents)
		}
	}
}
