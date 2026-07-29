package ui

import (
	"strings"
	"testing"
)

func TestTitleCase(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "single lowercase word",
			input: "hello",
			want:  "Hello",
		},
		{
			name:  "single uppercase word preserved",
			input: "API",
			want:  "API",
		},
		{
			name:  "all lowercase words title-cased",
			input: "create new branch",
			want:  "Create New Branch",
		},
		{
			name:  "acronym preserved among lowercase",
			input: "update UI settings",
			want:  "Update UI Settings",
		},
		{
			name:  "multiple acronyms preserved",
			input: "API and UI config",
			want:  "API And UI Config",
		},
		{
			name:  "mixed case word preserved (not all lowercase)",
			input: "iPhone setup",
			want:  "iPhone Setup",
		},
		{
			name:  "already title-cased word preserved",
			input: "Hello world",
			want:  "Hello World",
		},
		{
			name:  "single character word",
			input: "a b c",
			want:  "A B C",
		},
		{
			name:  "extra whitespace collapsed by Fields",
			input: "  hello   world  ",
			want:  "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := titleCase(tt.input)
			if got != tt.want {
				t.Errorf("titleCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRender_EmptyTitlePromotesSubtitle(t *testing.T) {
	card := NewCard(CardSuccess, "").Subtitle("promoted subtitle")
	rendered := card.Render()
	stripped := stripANSI(rendered)
	lines := splitRendered(stripped)

	// First line should have the glyph and the subtitle together.
	if len(lines) == 0 {
		t.Fatal("Render() produced no lines")
	}
	if !containsAll(lines[0], "✓", "promoted subtitle") {
		t.Errorf("first line should have glyph and subtitle; got %q", lines[0])
	}
}

func TestRender_EmptyTitlePromotesBody(t *testing.T) {
	card := NewCard(CardSuccess, "").KV("Key", "Value")
	rendered := card.Render()
	stripped := stripANSI(rendered)
	lines := splitRendered(stripped)

	if len(lines) == 0 {
		t.Fatal("Render() produced no lines")
	}
	// First line should have the glyph and KV content together.
	if !containsAll(lines[0], "✓", "Key", "Value") {
		t.Errorf("first line should have glyph and KV; got %q", lines[0])
	}
}

func TestRender_TitleAndSubtitle(t *testing.T) {
	card := NewCard(CardSuccess, "My Title").Subtitle("my subtitle")
	rendered := card.Render()
	stripped := stripANSI(rendered)
	lines := splitRendered(stripped)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines; got %d", len(lines))
	}
	if !containsAll(lines[0], "✓", "My Title") {
		t.Errorf("first line should have glyph and title; got %q", lines[0])
	}
	if !containsAll(lines[1], "my subtitle") {
		t.Errorf("second line should have subtitle; got %q", lines[1])
	}
}

func TestRender_ValueInline(t *testing.T) {
	card := NewCard(CardSuccess, "label").Value("val")
	rendered := card.Render()
	stripped := stripANSI(rendered)
	lines := splitRendered(stripped)

	if len(lines) == 0 {
		t.Fatal("Render() produced no lines")
	}
	if !containsAll(lines[0], "✓", "Label", "·", "val") {
		t.Errorf("first line should have glyph, title, dot separator, and value; got %q", lines[0])
	}
}

// splitRendered splits rendered output into non-empty trimmed lines.
func splitRendered(s string) []string {
	raw := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var lines []string
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// containsAll checks that s contains every substring.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestWrapForTimeline(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int // expected number of lines (-1 = just check > 1)
	}{
		{
			name:      "empty string returns single empty element",
			input:     "",
			wantCount: 1,
		},
		{
			name:      "short string fits in one line",
			input:     "hello world",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapForTimeline(tt.input)
			if tt.wantCount >= 0 && len(got) != tt.wantCount {
				t.Errorf("wrapForTimeline(%q): got %d lines, want %d", tt.input, len(got), tt.wantCount)
			}
		})
	}

	// Long string wraps to multiple lines. TermWidth() defaults to 80
	// in non-TTY test environments, so a string longer than
	// 80 - timelineConnWidth (75) should wrap.
	t.Run("long string wraps", func(t *testing.T) {
		// Build a string guaranteed to exceed the available width.
		long := ""
		for i := 0; i < 20; i++ {
			long += "longword "
		}
		got := wrapForTimeline(long)
		if len(got) < 2 {
			t.Errorf("wrapForTimeline(long): got %d lines, want >= 2", len(got))
		}
	})

	// Empty string returns a slice with one empty element.
	t.Run("empty string content", func(t *testing.T) {
		got := wrapForTimeline("")
		if len(got) != 1 || got[0] != "" {
			t.Errorf("wrapForTimeline(\"\"): got %v, want [\"\"]", got)
		}
	})
}


// TestMultiLineSubtitleKeepsConnector locks the timeline gutter on
// multi-line subtitles: every physical line after the card's first
// must carry the connector prefix (regression: an embedded-newline
// subtitle came back from wrapForTimeline as one element, so lines
// 2+ rendered flush at column 0 — every multi-line textarea record,
// e.g. PR body excerpts, broke the gutter).
func TestMultiLineSubtitleKeepsConnector(t *testing.T) {
	out := NewCard(CardSuccess, "Body").
		Subtitle("line one\nline two\nline three").
		Render()

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("expected 4 rendered lines, got %d:\n%s", len(lines), out)
	}
	for i, line := range lines[1:] {
		if !strings.Contains(line, "│") {
			t.Errorf("continuation line %d missing timeline connector: %q", i+2, line)
		}
	}
}
