package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// renderSummaryText composes a summary card's body string: muted
// total head followed by comma-joined non-zero segments, each
// styled with its segment color. The comma separator is muted.
// Used by both cardReporter.Summary and group.Summary so the two
// emit identical text.
func renderSummaryText(total string, segments []SummarySegment) string {
	mutedStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	parts := []string{mutedStyle.Render(total)}
	for _, s := range segments {
		if s.Count == 0 {
			continue
		}
		segStyle := lipgloss.NewStyle().Foreground(s.Color)
		parts = append(parts, segStyle.Render(fmt.Sprintf("%d %s", s.Count, s.Label)))
	}
	return strings.Join(parts, mutedStyle.Render(", "))
}

// summaryGlyphColor returns the color of the last non-zero segment,
// which becomes the summary card's glyph color. Callers order
// segments ascending by severity so the worst case dominates the
// glyph. Falls back to Palette.Muted when no segment has a non-zero
// count (e.g., an empty rollup).
func summaryGlyphColor(segments []SummarySegment) color.Color {
	glyphColor := color.Color(Palette.Muted)
	for _, s := range segments {
		if s.Count > 0 {
			glyphColor = s.Color
		}
	}
	return glyphColor
}
