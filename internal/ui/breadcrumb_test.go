package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBreadcrumb_RenderRow_SingleLine asserts that the rendered
// breadcrumb line is exactly one visible line.
func TestBreadcrumb_RenderRow_SingleLine(t *testing.T) {
	card := NewCard(CardRoot, "Bosun › Status").Breadcrumb("ProjectName")
	rendered := card.Render()
	// The breadcrumb is the last line of the logo box render.
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	last := lines[len(lines)-1]
	if got := lipgloss.Height(last); got != 1 {
		t.Errorf("breadcrumb line height = %d, want 1; line=%q", got, last)
	}
}

// TestBreadcrumb_RenderRow_Segments asserts that data segments and
// command tail appear in the rendered breadcrumb.
func TestBreadcrumb_RenderRow_Segments(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("Project")
	row := stripANSI(bc.RenderRow(80, []string{"Status"}))

	if !strings.Contains(row, "Project") {
		t.Errorf("expected Project segment; got %q", row)
	}
	if !strings.Contains(row, "Status") {
		t.Errorf("expected Status command segment; got %q", row)
	}
}

// TestBreadcrumb_RenderRow_Empty asserts that a breadcrumb with no
// segments and no command tail renders just the closing rule.
func TestBreadcrumb_RenderRow_Empty(t *testing.T) {
	bc := &breadcrumb{}
	row := stripANSI(bc.RenderRow(80, nil))
	if !strings.Contains(row, "╯") {
		t.Errorf("expected closing corner; got %q", row)
	}
}

// TestBreadcrumb_RenderCompactRow asserts the compact header includes
// Bosun root, data segments, and command path.
func TestBreadcrumb_RenderCompactRow(t *testing.T) {
	bc := &breadcrumb{}
	bc.AddSegment("MyProject")
	row := stripANSI(bc.RenderCompactRow(120, []string{"bosun", "Status"}))

	if !strings.Contains(row, "Bosun") {
		t.Errorf("expected Bosun root; got %q", row)
	}
	if !strings.Contains(row, "MyProject") {
		t.Errorf("expected MyProject segment; got %q", row)
	}
	if !strings.Contains(row, "Status") {
		t.Errorf("expected Status command; got %q", row)
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
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[':
			j := i + 2
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
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
