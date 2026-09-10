package cli

// Unit coverage for the gatherSelect flow's two pure renderers: the
// picker label and the record card. The full interactive flow
// (preload morph, form, gate validation) is exercised end-to-end by
// cleanup's e2e tests and visually by the ui package's PTY smoke.

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// TestGatherSelectLabel pins the picker rows' styling contract: bold
// name and dimmed brief via raw SGR intensity toggles, and — the
// part that breaks huh if violated — no lipgloss-style full SGR
// reset (\x1b[0m or bare \x1b[m), which would wipe huh's own
// selection/focus styling for the rest of the line.
func TestGatherSelectLabel(t *testing.T) {
	bare := gatherSelectLabel(gatherSelectItem{Name: "ws-a"})
	if bare != "\x1b[1mws-a\x1b[22m" {
		t.Errorf("bare label = %q, want the bolded name alone", bare)
	}

	annotated := gatherSelectLabel(gatherSelectItem{Name: "ws-b", Brief: "uncommitted changes (+1)"})
	if annotated != "\x1b[1mws-b\x1b[22m\x1b[2m · uncommitted changes (+1)\x1b[22m" {
		t.Errorf("annotated label = %q, want bold name + dimmed brief", annotated)
	}

	for _, label := range []string{bare, annotated} {
		if strings.Contains(label, "\x1b[0m") || strings.Contains(label, "\x1b[m") {
			t.Errorf("label %q carries a full SGR reset, which wipes huh's line styling", label)
		}
	}
}

// TestGatherSelectRecordCard pins the generic record renderer:
// selection tally in the title, rows in item order, selected rows
// with their glyph (defaulting to the success check) and detail,
// unselected rows fully receded behind the inactive glyph, and
// Detail falling back to Brief.
func TestGatherSelectRecordCard(t *testing.T) {
	items := []gatherSelectItem{
		{Name: "one"},
		{Name: "two", Brief: "brief-two"}, // no Detail → Brief in the record
		{Name: "three", Brief: "brief-three", Detail: "detail-three", Glyph: "X"},
	}
	gs := gatherSelect{Title: "things", N: len(items)}

	card := gs.recordCard(items, []int{0, 2})
	out := stripANSI(card.Render())
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	if !strings.Contains(out, "2 of 3 selected") {
		t.Errorf("card = %q, want the selection tally", out)
	}
	row := findRowContaining(t, lines, "one")
	if !strings.Contains(row, ui.Palette.Check) {
		t.Errorf("selected default-glyph row = %q, want the success check", row)
	}
	row = findRowContaining(t, lines, "two")
	if !strings.Contains(row, ui.Palette.Inactive) || !strings.Contains(row, "brief-two") {
		t.Errorf("unselected row = %q, want the inactive glyph and the Brief fallback", row)
	}
	row = findRowContaining(t, lines, "three")
	if !strings.Contains(row, "X") || !strings.Contains(row, "detail-three") {
		t.Errorf("selected custom-glyph row = %q, want the caller's glyph and Detail", row)
	}
	if strings.Contains(out, "brief-three") {
		t.Errorf("card = %q, Brief leaked into a row that has a Detail", out)
	}

	// Item order holds regardless of selection.
	oneIdx := -1
	threeIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "one") && oneIdx == -1 {
			oneIdx = i
		}
		if strings.Contains(l, "three") {
			threeIdx = i
		}
	}
	if oneIdx > threeIdx {
		t.Errorf("rows reordered (one %d, three %d), want item order", oneIdx, threeIdx)
	}

	// RecordState drives the card state; nil defaults to success —
	// pin the wired case.
	gs.RecordState = func() ui.CardState { return ui.CardFailed }
	stateOut := stripANSI(gs.recordCard(items, nil).Render())
	if !strings.Contains(stateOut, "0 of 3 selected") {
		t.Errorf("empty-selection card = %q, want the zero tally", stateOut)
	}
}
