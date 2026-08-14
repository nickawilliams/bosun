package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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

// TestCardEmitToReporter locks the Reporter routing contract:
// each terminal state maps to the correct Reporter method,
// subtitle takes priority over value, and transient/input states
// stay silent.
func TestCardEmitToReporter(t *testing.T) {
	cases := []struct {
		name       string
		card       *Card
		wantKind   string
		wantLabel  string
		wantValue  string
		wantSilent bool
	}{
		{
			name:      "CardSuccess title-only → Complete",
			card:      NewCard(CardSuccess, "preview"),
			wantKind:  CaptureComplete,
			wantLabel: "preview",
		},
		{
			name:      "CardSuccess value → CompleteValue",
			card:      NewCard(CardSuccess, "preview").Value("staging"),
			wantKind:  CaptureComplete,
			wantLabel: "preview",
			wantValue: "staging",
		},
		{
			name:      "CardSuccess subtitle → CompleteValue (subtitle wins)",
			card:      NewCard(CardSuccess, "preview").Subtitle("prod-env"),
			wantKind:  CaptureComplete,
			wantLabel: "preview",
			wantValue: "prod-env",
		},
		{
			name:      "CardReady title-only → Complete",
			card:      NewCard(CardReady, "workspace"),
			wantKind:  CaptureComplete,
			wantLabel: "workspace",
		},
		{
			name:      "CardFailed title-only → Fail",
			card:      NewCard(CardFailed, "build"),
			wantKind:  CaptureFail,
			wantLabel: "build",
		},
		{
			name:      "CardFailed value → FailValue",
			card:      NewCard(CardFailed, "build").Value("missing binary"),
			wantKind:  CaptureFail,
			wantLabel: "build",
			wantValue: "missing binary",
		},
		{
			name:      "CardSkipped title-only → Skip",
			card:      NewCard(CardSkipped, "lint"),
			wantKind:  CaptureSkip,
			wantLabel: "lint",
		},
		{
			name:      "CardSkipped value → SkipValue",
			card:      NewCard(CardSkipped, "lint").Value("not configured"),
			wantKind:  CaptureSkip,
			wantLabel: "lint",
			wantValue: "not configured",
		},
		{
			name:      "CardInfo → Info",
			card:      NewCard(CardInfo, "running tests"),
			wantKind:  CaptureInfo,
			wantLabel: "running tests",
		},
		{
			name:      "CardData → Info",
			card:      NewCard(CardData, "schema snapshot"),
			wantKind:  CaptureInfo,
			wantLabel: "schema snapshot",
		},
		{
			name:       "CardInput → silent (form header, raw mode never runs prompts)",
			card:       NewCard(CardInput, "select workspace"),
			wantSilent: true,
		},
		{
			name:       "CardRunning → silent (transient)",
			card:       NewCard(CardRunning, "building"),
			wantSilent: true,
		},
		{
			name:       "CardPending → silent (transient)",
			card:       NewCard(CardPending, "waiting"),
			wantSilent: true,
		},
		{
			name:       "CardWaiting → silent (transient)",
			card:       NewCard(CardWaiting, "ci running"),
			wantSilent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := NewCaptureReporter()
			tc.card.EmitToReporter(cap)
			evts := cap.Events()
			if tc.wantSilent {
				if len(evts) != 0 {
					t.Errorf(
						"expected silent, got %d events: %s",
						len(evts), cap.Dump(),
					)
				}
				return
			}
			if len(evts) != 1 {
				t.Fatalf(
					"got %d events, want 1: %s",
					len(evts), cap.Dump(),
				)
			}
			e := evts[0]
			if e.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", e.Kind, tc.wantKind)
			}
			if e.Label != tc.wantLabel {
				t.Errorf("Label = %q, want %q", e.Label, tc.wantLabel)
			}
			if e.Value != tc.wantValue {
				t.Errorf("Value = %q, want %q", e.Value, tc.wantValue)
			}
		})
	}
}

// TestCardValueWrapsAtValueColumn locks value-line width handling: a
// value line wider than the space left of the value column wraps
// inside the renderer with fragments aligned under the first value
// character (regression: continuation lines were appended untouched,
// so an overlong line physically wrapped in the terminal — breaking
// the gutter and the rewind's line count, which trusts strings.Count
// of the render).
func TestCardValueWrapsAtValueColumn(t *testing.T) {
	// TermWidth() == 80 in tests. Title "plan" renders as "Plan"
	// (width 4), so the value column starts at 4+3=7 and wraps at 68.
	long := strings.Repeat("alpha beta ", 12) // ~132 cells
	rendered := ansi.Strip(NewCard(CardSuccess, "plan").Value(long).Render())

	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected the value to wrap onto continuation lines:\n%s", rendered)
	}
	for _, l := range lines {
		if w := len([]rune(l)); w > 80 {
			t.Errorf("line exceeds terminal width (%d): %q", w, l)
		}
	}
	// Continuation lines align under the first value character: the
	// connector prefix, then alignW+3 = 7 spaces of padding.
	for _, l := range lines[1:] {
		if !strings.Contains(l, "alpha") {
			continue
		}
		trimmed := strings.TrimLeft(l, " │")
		indent := len([]rune(l)) - len([]rune(trimmed))
		if indent < 7 {
			t.Errorf("continuation line lost the value column (indent %d): %q", indent, l)
		}
	}
}
