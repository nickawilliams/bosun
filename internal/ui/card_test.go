package ui

import "testing"

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
