package cli

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// TestPlanGateChromeFitsTheFrame is the assertion planGateChrome is a
// measurement of, not a guess: the widest plan planGateFits still
// accepts must produce a live frame — connector, card, and huh's own
// trailing rows — that fits on screen. It fails if huh's confirm
// chrome grows, which is exactly when the constant needs revisiting;
// without it the gate would go back to cycling against rows the
// inline renderer had dropped (#69/#98).
func TestPlanGateChromeFitsTheFrame(t *testing.T) {
	bufferedTerm(t)

	// Grow a plan one row at a time to the largest planGateFits
	// still accepts, then measure what that plan actually paints.
	build := func(rows int) *ui.PlanCard {
		p := ui.NewPlan()
		for i := 0; i < rows; i++ {
			p.Add(ui.PlanCreate, "add", "worktree", "host-ui", "feature/EX-1_slug")
		}
		return ui.NewPlanCard(p)
	}
	widest := 1
	for n := 1; n <= ui.TermHeight()+2; n++ {
		if !planGateFits(build(n)) {
			break
		}
		widest = n
	}
	pc := build(widest)
	if !planGateFits(pc) {
		t.Fatalf("widest accepted plan (%d rows) doesn't fit — the search above is wrong", widest)
	}

	var confirmed bool
	frame := formFirstFrame(newPlanConfirm(&confirmed))
	if !strings.HasSuffix(frame, "\n") {
		frame += "\n"
	}

	card := pc.Render()
	if !strings.HasSuffix(card, "\n") {
		card += "\n"
	}

	// What the session paints as the gate's frame: the connector
	// row, the plan card, then the form.
	rows := 1 + strings.Count(card, "\n") + strings.Count(frame, "\n")

	if rows > ui.TermHeight() {
		t.Errorf("gate frame is %d rows on a %d-row terminal — planGateChrome (%d) no longer covers huh's chrome",
			rows, ui.TermHeight(), planGateChrome)
	}
}

// TestPlanGateFitsBound pins the branch planGateFits selects: a small
// plan cycles with its rows inside the live frame, while a plan
// taller than the screen takes the commit-the-rows-first path so the
// inline renderer never sees an oversized frame.
func TestPlanGateFitsBound(t *testing.T) {
	bufferedTerm(t)

	small := ui.NewPlan().Add(ui.PlanCreate, "add", "worktree", "host-ui", "feature/EX-1_slug")
	if !planGateFits(ui.NewPlanCard(small)) {
		t.Errorf("a one-row plan must cycle in place on a %d-row terminal", ui.TermHeight())
	}

	tall := ui.NewPlan()
	for i := 0; i < ui.TermHeight()+1; i++ {
		tall.Add(ui.PlanCreate, "add", "worktree", "host-ui", "feature/EX-1_slug")
	}
	if planGateFits(ui.NewPlanCard(tall)) {
		t.Errorf("a plan taller than the %d-row terminal must not enter the live frame", ui.TermHeight())
	}
}
