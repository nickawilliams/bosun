package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBreadcrumb_LineCount locks down the load-bearing invariant for
// the partial-rewrite squish path: typical root cards must produce a
// breadcrumb that occupies exactly one terminal line. If this ever
// returns >1, the squish mechanism falls back to the full-erase path
// (correct but reintroduces the visible flash).
func TestBreadcrumb_LineCount(t *testing.T) {
	cases := []struct {
		name string
		card *Card
	}{
		{name: "no segments", card: NewCard(CardRoot, "Bosun")},
		{name: "command tail only", card: NewCard(CardRoot, "Bosun › Status")},
		{
			name: "command tail + data segment",
			card: NewCard(CardRoot, "Bosun › Status").Breadcrumb("ProjectName"),
		},
		{
			name: "command tail + multiple data segments",
			card: NewCard(CardRoot, "Bosun › Status").
				Breadcrumb("ProjectName").
				Breadcrumb("PROJ-123"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.card.BreadcrumbLineCount(); got != 1 {
				t.Errorf("BreadcrumbLineCount() = %d, want 1", got)
			}
		})
	}

	// Non-root cards should report 0 lines.
	t.Run("non-root card", func(t *testing.T) {
		if got := NewCard(CardSuccess, "Hello").BreadcrumbLineCount(); got != 0 {
			t.Errorf("BreadcrumbLineCount() for non-root = %d, want 0", got)
		}
	})
}

// TestBreadcrumb_RenderRow_SingleLine asserts that the rendered
// breadcrumb line, after stripping its trailing newline, is exactly
// one visible line. Bubbletea's spinner frame in partial mode hands
// off this string verbatim; an extra newline would push the cursor
// and reintroduce the flash.
func TestBreadcrumb_RenderRow_SingleLine(t *testing.T) {
	card := NewCard(CardRoot, "Bosun › Status").Breadcrumb("ProjectName")

	line := card.RenderBreadcrumbLine()
	if line == "" {
		t.Fatal("RenderBreadcrumbLine returned empty for root card")
	}
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("expected trailing newline; got %q", line)
	}
	stripped := strings.TrimRight(line, "\n")
	if got := lipgloss.Height(stripped); got != 1 {
		t.Errorf("breadcrumb line height = %d, want 1; line=%q", got, stripped)
	}
}

// TestBreadcrumb_RenderRow_NonRoot asserts that the method only
// returns content for root cards (the squish mechanism only ever
// asks for breadcrumb lines from roots).
func TestBreadcrumb_RenderRow_NonRoot(t *testing.T) {
	if got := NewCard(CardSuccess, "Hello").RenderBreadcrumbLine(); got != "" {
		t.Errorf("non-root RenderBreadcrumbLine = %q, want empty", got)
	}
}

// stripANSI removes ANSI escape sequences so we can match against
// the visible content of a rendered string.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Skip an ESC sequence.
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			i = j + 1
		case ']':
			j := strings.Index(s[i:], "\x1b\\")
			if j < 0 {
				return b.String()
			}
			i += j + 2
		default:
			i += 2
		}
	}
	return b.String()
}

// TestBreadcrumb_TrailingSlot_GlyphOnly asserts that setting only the
// trailing glyph (with empty title) renders the slot as just the glyph
// after a › separator.
func TestBreadcrumb_TrailingSlot_GlyphOnly(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")
	bc.SetTrailing("", "⠹", false)

	row := stripANSI(bc.RenderRow(80, []string{"Status"}))
	if !strings.Contains(row, "Project") {
		t.Errorf("expected Project segment; got %q", row)
	}
	if !strings.Contains(row, "Status") {
		t.Errorf("expected Status command segment; got %q", row)
	}
	if !strings.Contains(row, "⠹") {
		t.Errorf("expected glyph in trailing slot; got %q", row)
	}
}

// TestBreadcrumb_TrailingSlot_TitleOnly asserts that setting only the
// trailing title (with empty glyph) renders the slot as just the title
// after a › separator.
func TestBreadcrumb_TrailingSlot_TitleOnly(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")
	bc.SetTrailing("Issue Title Here", "", false)

	row := stripANSI(bc.RenderRow(80, []string{"Status"}))
	if !strings.Contains(row, "Issue Title Here") {
		t.Errorf("expected trailing title; got %q", row)
	}
}

// TestBreadcrumb_TrailingSlot_GlyphAndTitle asserts both glyph and
// title appear in the trailing slot, separated by a space.
func TestBreadcrumb_TrailingSlot_GlyphAndTitle(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")
	bc.SetTrailing("Done Now", "✓", false)

	row := stripANSI(bc.RenderRow(80, []string{"Status"}))
	if !strings.Contains(row, "✓ Done Now") {
		t.Errorf("expected glyph then space then title; got %q", row)
	}
}

// TestBreadcrumb_TrailingSlot_Empty asserts that with no trailing
// content, no separator and no slot are rendered.
func TestBreadcrumb_TrailingSlot_Empty(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")

	row := stripANSI(bc.RenderRow(80, []string{"Status"}))
	// Should have one › between Project and Status, not two.
	count := strings.Count(row, "›")
	if count != 1 {
		t.Errorf("expected exactly 1 › separator; got %d in %q", count, row)
	}
}

// TestBreadcrumb_TrailingSlot_SuppressGlyph asserts that suppress=true
// drops the glyph regardless of what was passed.
func TestBreadcrumb_TrailingSlot_SuppressGlyph(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")
	bc.SetTrailing("Title", "✓", true)

	row := stripANSI(bc.RenderRow(80, []string{"Status"}))
	if strings.Contains(row, "✓") {
		t.Errorf("expected glyph to be suppressed; got %q", row)
	}
	if !strings.Contains(row, "Title") {
		t.Errorf("expected title still present; got %q", row)
	}
}
