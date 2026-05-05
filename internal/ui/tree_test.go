package ui

import (
	"strings"
	"testing"
)

func TestTree_Render_Empty(t *testing.T) {
	tr := NewTree()
	if got := tr.Render(); got != "" {
		t.Errorf("empty tree Render() = %q, want %q", got, "")
	}
}

func TestTree_Render_LeafOnly(t *testing.T) {
	tr := NewTree().Add(
		Leaf("●", Palette.Primary, "host", "localhost"),
		Leaf("●", Palette.Success, "port", "8080"),
	)
	out := tr.Render()

	if out == "" {
		t.Fatal("Render() returned empty string for leaf-only tree")
	}

	// Should contain box-drawing branch characters.
	if !strings.Contains(out, "├") && !strings.Contains(out, "└") {
		t.Error("leaf-only tree should contain box-drawing branch characters")
	}

	// Should contain the key names.
	if !strings.Contains(out, "host") {
		t.Error("missing key 'host' in rendered output")
	}
	if !strings.Contains(out, "port") {
		t.Error("missing key 'port' in rendered output")
	}

	// Should contain the values.
	if !strings.Contains(out, "localhost") {
		t.Error("missing value 'localhost' in rendered output")
	}
	if !strings.Contains(out, "8080") {
		t.Error("missing value '8080' in rendered output")
	}

	// Should contain the dot separator.
	if !strings.Contains(out, Palette.Dot) {
		t.Error("missing dot separator in rendered output")
	}

	// Last node uses └, not ├.
	lines := splitNonEmptyLines(out)
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "└") {
		t.Errorf("last line should use └ connector, got: %q", lastLine)
	}
}

func TestTree_Render_GroupWithChildren(t *testing.T) {
	tr := NewTree().Add(
		Group("database",
			Leaf("●", Palette.Success, "host", "db.example.com"),
			Leaf("●", Palette.Success, "port", "5432"),
		),
	)
	out := tr.Render()

	if out == "" {
		t.Fatal("Render() returned empty string for group tree")
	}

	// Should contain the group key.
	if !strings.Contains(out, "database") {
		t.Error("missing group key 'database' in rendered output")
	}

	// Should contain child keys.
	if !strings.Contains(out, "host") {
		t.Error("missing child key 'host' in rendered output")
	}
	if !strings.Contains(out, "port") {
		t.Error("missing child key 'port' in rendered output")
	}

	// Children should produce more lines than just the group header.
	lines := splitNonEmptyLines(out)
	if len(lines) < 3 {
		t.Errorf("group tree should have at least 3 lines (group + 2 children), got %d", len(lines))
	}
}

func TestTree_Render_NestedGroups(t *testing.T) {
	tr := NewTree().Add(
		Group("server",
			Group("database",
				Leaf("●", Palette.Success, "host", "db.local"),
			),
			Leaf("●", Palette.Primary, "port", "3000"),
		),
	)
	out := tr.Render()

	if out == "" {
		t.Fatal("Render() returned empty string for nested tree")
	}

	// Should contain all keys at every depth.
	for _, key := range []string{"server", "database", "host", "port"} {
		if !strings.Contains(out, key) {
			t.Errorf("missing key %q in rendered output", key)
		}
	}

	// Nested output produces vertical connectors for indentation.
	if !strings.Contains(out, "│") {
		t.Error("nested tree should contain │ vertical connector for indentation")
	}
}

func TestTree_Render_KeyValueAlignment(t *testing.T) {
	tr := NewTree().Add(
		Leaf("●", Palette.Success, "short", "a"),
		Leaf("●", Palette.Success, "much-longer-key", "b"),
	)
	out := tr.Render()
	lines := splitNonEmptyLines(out)

	// Both lines should contain the dot separator and the dots
	// should be at the same column position (global alignment).
	dotPositions := make([]int, 0, 2)
	for _, line := range lines {
		idx := strings.Index(line, Palette.Dot)
		if idx >= 0 {
			dotPositions = append(dotPositions, idx)
		}
	}
	if len(dotPositions) != 2 {
		t.Fatalf("expected 2 lines with dot separator, got %d", len(dotPositions))
	}
	if dotPositions[0] != dotPositions[1] {
		t.Errorf("dot separators not aligned: positions %d and %d", dotPositions[0], dotPositions[1])
	}
}

// splitNonEmptyLines splits a string by newlines and filters out empty lines.
func splitNonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
