package slack

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// truncate feeds Slack mrkdwn blocks, so two properties matter beyond the
// visible output: the result never exceeds the byte budget, and it is always
// valid UTF-8. An earlier version honored neither — it appended a three-byte
// ellipsis on top of the budget and cut without regard to rune boundaries.
func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under budget is untouched", "hello", 10, "hello"},
		{"exactly at budget is untouched", "hello", 5, "hello"},
		{"ascii cut leaves room for the marker", "hello world", 8, "hello…"},
		{"budget of exactly the marker", "hello", 3, "…"},
		{"budget below the marker hard-cuts", "hello", 2, "he"},
		{"zero budget is empty", "hello", 0, ""},
		{"negative budget is empty", "hello", -1, ""},

		// The cut lands inside a multi-byte rune; it must step back rather
		// than emit half of one. "café" is 5 bytes — 'é' spans bytes 3-4.
		{"cut inside a rune steps back", "café au lait", 6, "caf…"},
		{"all multi-byte runes", "→→→→→", 7, "→…"},
		// Budget leaves one byte after the marker, but the first rune needs
		// three — no content fits, and the marker wins over dropping it.
		{"no whole rune fits beside the marker", "✓ ok", 4, "…"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.max)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
			if tc.max > 0 && len(got) > tc.max {
				t.Errorf("result is %d bytes, over the %d-byte budget", len(got), tc.max)
			}
			if !utf8.ValidString(got) {
				t.Errorf("result %q is not valid UTF-8", got)
			}
		})
	}
}

// Sweeping every budget against text with multi-byte runes at varied offsets
// catches boundary handling that a handful of hand-picked cases can miss.
func TestTruncateNeverExceedsBudgetOrSplitsRunes(t *testing.T) {
	inputs := []string{
		"plain ascii text here",
		"café au lait — a café, naturally",
		"→→→→→→→→→→",
		"✓ done — shipped ❭ the thing",
		strings.Repeat("é", 20),
	}
	for _, in := range inputs {
		for max := range len(in) + 4 {
			got := truncate(in, max)
			if len(got) > max {
				t.Fatalf("truncate(%q, %d) = %q: %d bytes exceeds budget",
					in, max, got, len(got))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%q, %d) = %q: invalid UTF-8", in, max, got)
			}
		}
	}
}
