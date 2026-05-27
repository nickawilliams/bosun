package ui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestLerpColors(t *testing.T) {
	a := lipgloss.Color("#000000")
	b := lipgloss.Color("#ffffff")

	t.Run("single element returns first color", func(t *testing.T) {
		colors := lerpColors(a, b, 1)
		if len(colors) != 1 {
			t.Fatalf("len = %d, want 1", len(colors))
		}
	})

	t.Run("two elements returns endpoints", func(t *testing.T) {
		colors := lerpColors(a, b, 2)
		if len(colors) != 2 {
			t.Fatalf("len = %d, want 2", len(colors))
		}
		assertColorNear(t, colors[0], 0, 0, 0)
		assertColorNear(t, colors[1], 255, 255, 255)
	})

	t.Run("midpoint is interpolated", func(t *testing.T) {
		colors := lerpColors(a, b, 3)
		if len(colors) != 3 {
			t.Fatalf("len = %d, want 3", len(colors))
		}
		assertColorNear(t, colors[1], 127, 127, 127)
	})

	t.Run("zero elements returns first color", func(t *testing.T) {
		colors := lerpColors(a, b, 0)
		if len(colors) != 1 {
			t.Fatalf("len = %d, want 1", len(colors))
		}
	})
}

func assertColorNear(t *testing.T, c color.Color, wantR, wantG, wantB uint8) {
	t.Helper()
	r, g, b, _ := c.RGBA()
	gotR, gotG, gotB := uint8(r>>8), uint8(g>>8), uint8(b>>8)
	const tolerance = 3
	if diff(gotR, wantR) > tolerance || diff(gotG, wantG) > tolerance || diff(gotB, wantB) > tolerance {
		t.Errorf("color = (%d,%d,%d), want near (%d,%d,%d)", gotR, gotG, gotB, wantR, wantG, wantB)
	}
}

func diff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestSpacerPrefix(t *testing.T) {
	t.Cleanup(func() { needsSpacer = false })

	// Initially false — first call returns empty and arms.
	needsSpacer = false
	if got := spacerPrefix(); got != "" {
		t.Errorf("first call: got %q, want empty", got)
	}

	// Now armed — second call returns the connector line.
	if got := spacerPrefix(); got == "" {
		t.Error("second call: got empty, want connector line")
	}

	// Auto-rearmed — third call also returns connector.
	if got := spacerPrefix(); got == "" {
		t.Error("third call: got empty, want connector line")
	}

	// ClearSpacer suppresses.
	ClearSpacer()
	if got := spacerPrefix(); got != "" {
		t.Errorf("after ClearSpacer: got %q, want empty", got)
	}
}
