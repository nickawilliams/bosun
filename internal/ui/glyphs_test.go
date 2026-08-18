package ui

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
)

// The whole reason box-drawing chrome is excluded from the themeable
// palette is that layout math assumes each character occupies exactly
// one terminal cell: rules are drawn with strings.Repeat and callers
// subtract fixed chrome widths from the available columns. A
// substitute that measured 0 (combining mark) or 2 (East Asian wide)
// would skew every rule in the app without failing to compile.
func TestBoxDrawingCharsAreOneCellWide(t *testing.T) {
	for name, ch := range map[string]string{
		"BoxVertical":   BoxVertical,
		"BoxHorizontal": BoxHorizontal,
		"BoxCornerTL":   BoxCornerTL,
		"BoxCornerTR":   BoxCornerTR,
		"BoxCornerBR":   BoxCornerBR,
		"BoxCornerBL":   BoxCornerBL,
		"BoxTee":        BoxTee,
		"BoxElbow":      BoxElbow,
	} {
		if got := lipgloss.Width(ch); got != 1 {
			t.Errorf("%s (%q) is %d cells wide, want 1 — layout math assumes single-width", name, ch, got)
		}
	}
}

// The tree renderer stacks one connector per nesting level to build a
// row's prefix, so they must all measure the same. If treeBranch were
// a cell wider than treeBlank, sibling nodes at the same depth would
// render at different columns.
func TestTreeConnectorsShareIndentWidth(t *testing.T) {
	for name, conn := range map[string]string{
		"treeBranch": treeBranch,
		"treeLast":   treeLast,
		"treeDown":   treeDown,
		"treeBlank":  treeBlank,
	} {
		if got := lipgloss.Width(conn); got != treeIndentWidth {
			t.Errorf("%s (%q) is %d cells wide, want treeIndentWidth (%d)",
				name, conn, got, treeIndentWidth)
		}
	}
}

// Every card state must resolve to a glyph and a color. A state added
// to the CardState enum but not to stateGlyph falls through to the
// blank gutter — the card still renders, so nothing fails except the
// missing glyph, which is easy to miss in review.
func TestStateGlyphCoversEveryCardState(t *testing.T) {
	states := map[CardState]string{
		CardPending: "CardPending",
		CardRunning: "CardRunning",
		CardSuccess: "CardSuccess",
		CardSkipped: "CardSkipped",
		CardFailed:  "CardFailed",
		CardInfo:    "CardInfo",
		CardInput:   "CardInput",
		CardRoot:    "CardRoot",
		CardData:    "CardData",
		CardReady:   "CardReady",
		CardWaiting: "CardWaiting",
	}

	// Bound the sweep with the sentinel rather than the last real
	// state, so a state *appended* to the enum is caught too — this
	// enum has grown by appending before (CardData, CardReady and
	// CardWaiting all sit after CardRoot).
	for s := CardPending; s < cardStateCount; s++ {
		if _, named := states[s]; !named {
			t.Fatalf("CardState(%d) is declared but missing from this test — add it and check stateGlyph handles it", s)
		}
	}
	if len(states) != int(cardStateCount) {
		t.Fatalf("test names %d states but the enum declares %d", len(states), cardStateCount)
	}

	for state, name := range states {
		glyph, fg, ok := stateGlyph(state)
		if !ok {
			t.Errorf("%s: stateGlyph reported no glyph, want one", name)
			continue
		}
		if glyph == "" {
			t.Errorf("%s: glyph is empty", name)
		}
		if fg == nil {
			t.Errorf("%s: color is nil", name)
		}
		if w := lipgloss.Width(glyph); w != 1 {
			t.Errorf("%s: glyph %q is %d cells wide, want 1 — the gutter is one column", name, glyph, w)
		}
	}
}

// The mapping itself, pinned. Coverage of "every state has *a*
// glyph" does not catch a state repointed to the wrong one — and a
// mechanical sweep that rewrote this switch is exactly the change
// that could do that silently. Assert the pairs.
func TestStateGlyphMapping(t *testing.T) {
	p := defaultPalette()
	for _, tc := range []struct {
		name      string
		state     CardState
		wantGlyph string
		wantColor color.Color
	}{
		{"CardPending", CardPending, p.Pending, p.Muted},
		{"CardRunning", CardRunning, p.Pending, p.Primary},
		{"CardSuccess", CardSuccess, p.Check, p.Success},
		{"CardSkipped", CardSkipped, p.Attention, p.Warning},
		{"CardFailed", CardFailed, p.Cross, p.Error},
		{"CardInfo", CardInfo, p.Active, p.Primary},
		{"CardInput", CardInput, p.Unknown, p.Accent},
		{"CardRoot", CardRoot, BoxCornerTL, p.Recessed},
		{"CardData", CardData, p.Active, p.Primary},
		// Ready is ● not ✓: the work is good but not finished, so it
		// keeps the active shape and borrows the success color.
		{"CardReady", CardReady, p.Active, p.Success},
		{"CardWaiting", CardWaiting, p.Waiting, p.Info},
	} {
		t.Run(tc.name, func(t *testing.T) {
			glyph, fg, ok := stateGlyph(tc.state)
			if !ok {
				t.Fatalf("stateGlyph reported no glyph")
			}
			if glyph != tc.wantGlyph {
				t.Errorf("glyph = %q, want %q", glyph, tc.wantGlyph)
			}
			if fg != tc.wantColor {
				t.Errorf("color = %v, want %v", fg, tc.wantColor)
			}
		})
	}
}

func TestStateGlyphUnknownStateHasNoGlyph(t *testing.T) {
	glyph, fg, ok := stateGlyph(CardState(999))
	if ok {
		t.Errorf("unknown state: ok = true, want false")
	}
	if glyph != "" || fg != nil {
		t.Errorf("unknown state: got (%q, %v), want (\"\", nil)", glyph, fg)
	}
}

// stateGlyph must read the live palette rather than capturing symbols
// at init. This is the property the whole consolidation buys: swap
// the palette's symbol set and every card follows. A call site that
// snapshotted the glyph into a package var would pass every other
// test in this file and fail only here.
func TestStateGlyphFollowsPaletteSwaps(t *testing.T) {
	original := Palette
	t.Cleanup(func() { Palette = original })

	Palette.Check = "Y"
	Palette.Cross = "N"

	if glyph, _, _ := stateGlyph(CardSuccess); glyph != "Y" {
		t.Errorf("CardSuccess glyph = %q, want %q — stateGlyph is not reading the live palette", glyph, "Y")
	}
	if glyph, _, _ := stateGlyph(CardFailed); glyph != "N" {
		t.Errorf("CardFailed glyph = %q, want %q — stateGlyph is not reading the live palette", glyph, "N")
	}
}

// Group nodes take their glyph from the palette at construction time,
// so the same swap has to reach the tree.
func TestGroupGlyphFollowsPaletteSwaps(t *testing.T) {
	original := Palette
	t.Cleanup(func() { Palette = original })

	Palette.Inactive = "o"

	if got := Group("cfg").Glyph; got != "o" {
		t.Errorf("Group glyph = %q, want %q — Group is not reading the live palette", got, "o")
	}
}
