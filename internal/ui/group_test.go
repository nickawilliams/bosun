package ui

import (
	"strings"
	"testing"
)

func TestGroupAggregate(t *testing.T) {
	tests := []struct {
		name   string
		counts groupCounts
		want   CardState
	}{
		{
			name:   "any failure dominates",
			counts: groupCounts{success: 2, skipped: 1, failed: 1},
			want:   CardFailed,
		},
		{
			name:   "single failure",
			counts: groupCounts{failed: 1},
			want:   CardFailed,
		},
		{
			name:   "success and skipped mix yields success",
			counts: groupCounts{success: 1, skipped: 2},
			want:   CardSuccess,
		},
		{
			name:   "all skipped yields skipped",
			counts: groupCounts{skipped: 3},
			want:   CardSkipped,
		},
		{
			name:   "info-only collapses to success (info does not propagate)",
			counts: groupCounts{info: 2},
			want:   CardSuccess,
		},
		{
			name:   "success plus info yields success, info ignored",
			counts: groupCounts{success: 1, info: 2},
			want:   CardSuccess,
		},
		{
			name:   "info plus skipped yields skipped (info does not propagate)",
			counts: groupCounts{skipped: 1, info: 2},
			want:   CardSkipped,
		},
		{
			name:   "empty group collapses to success",
			counts: groupCounts{},
			want:   CardSuccess,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &group{counts: tt.counts}
			if got := g.aggregate(); got != tt.want {
				t.Errorf("aggregate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCardIndentRender(t *testing.T) {
	c := NewCard(CardSuccess, "child").Indent(1)
	out := c.Render()
	if out == "" {
		t.Fatal("Render() returned empty string")
	}
	// Every non-empty line should contain the timeline connector "│"
	// from the indent prefix (styled with ANSI codes).
	for _, line := range splitTrimmedLines(out) {
		if !strings.Contains(line, "│") {
			t.Errorf("line missing timeline connector: %q", line)
		}
	}

	// Indent(0) should match plain Render output.
	plain := NewCard(CardSuccess, "child").Render()
	zero := NewCard(CardSuccess, "child").Indent(0).Render()
	if plain != zero {
		t.Errorf("Indent(0) changed render output\n plain: %q\n  zero: %q", plain, zero)
	}
}

func splitTrimmedLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
