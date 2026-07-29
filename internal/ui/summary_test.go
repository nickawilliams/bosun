package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestRenderSummaryText locks the summary body grammar: muted total
// head, comma-joined non-zero segments, zero-count segments omitted.
func TestRenderSummaryText(t *testing.T) {
	segments := []SummarySegment{
		{Count: 3, Label: "passed", Color: Palette.Success},
		{Count: 0, Label: "warnings", Color: Palette.Warning},
		{Count: 1, Label: "failed", Color: Palette.Error},
	}
	got := ansi.Strip(renderSummaryText("4 checks", segments))
	want := "4 checks, 3 passed, 1 failed"
	if got != want {
		t.Errorf("renderSummaryText() = %q, want %q", got, want)
	}
	if strings.Contains(got, "warnings") {
		t.Errorf("zero-count segment rendered: %q", got)
	}
}

// TestSummaryGlyphColor locks the worst-segment-wins glyph rule:
// callers order segments ascending by severity, and the last
// non-zero one colors the glyph; an all-zero rollup falls back to
// muted.
func TestSummaryGlyphColor(t *testing.T) {
	segs := []SummarySegment{
		{Count: 2, Label: "ok", Color: Palette.Success},
		{Count: 1, Label: "warn", Color: Palette.Warning},
		{Count: 0, Label: "fail", Color: Palette.Error},
	}
	if got := summaryGlyphColor(segs); got != Palette.Warning {
		t.Errorf("glyph color = %v, want the last non-zero segment (warning)", got)
	}
	if got := summaryGlyphColor([]SummarySegment{{Count: 0, Color: Palette.Error}}); got != Palette.Muted {
		t.Errorf("all-zero rollup glyph = %v, want muted fallback", got)
	}
}
