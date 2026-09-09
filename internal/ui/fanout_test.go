package ui

import (
	"fmt"
	"strings"
	"testing"
)

// TestGroupModelSlots drives the render model directly with the slot
// message vocabulary and pins the in-place contract: a slot's row
// position is fixed at start time, resolutions land at that position
// regardless of completion order, and unresolved slots keep a
// running-indicator row.
func TestGroupModelSlots(t *testing.T) {
	m := newGroupModel("parent", 0, nil)
	m.processMsg(groupSlotStartMsg{title: "alpha", indent: 1, slot: 11})
	m.processMsg(groupSlotStartMsg{title: "beta", indent: 1, slot: 12})

	// The second slot resolves first — its row must still render
	// after the first slot's (still running) indicator.
	m.processMsg(groupChildMsg{rendered: "ROW-BETA\n", state: CardSuccess, slot: 12})
	m.processMsg(groupSlotDoneMsg{slot: 12})

	view := m.viewString()
	alphaAt := strings.Index(view, "Alpha")
	betaAt := strings.Index(view, "ROW-BETA")
	if alphaAt < 0 || betaAt < 0 {
		t.Fatalf("mid-flight view missing rows (alpha %d, beta %d):\n%s", alphaAt, betaAt, view)
	}
	if alphaAt > betaAt {
		t.Errorf("slot order not preserved: running alpha at %d renders after resolved beta at %d\n%s", alphaAt, betaAt, view)
	}

	// First slot resolves with a failure; counts tally per state and
	// the group finalizes to the worst child outcome.
	m.processMsg(groupChildMsg{rendered: "ROW-ALPHA\n", state: CardFailed, slot: 11})
	m.processMsg(groupSlotDoneMsg{slot: 11})
	m.processMsg(groupDoneMsg{})

	view = m.viewString()
	if a, b := strings.Index(view, "ROW-ALPHA"), strings.Index(view, "ROW-BETA"); a < 0 || b < 0 || a > b {
		t.Errorf("resolved rows out of slot order (alpha %d, beta %d):\n%s", a, b, view)
	}
	if strings.Contains(view, "Alpha") {
		t.Errorf("resolved slot still renders its running row:\n%s", view)
	}
	if m.root.finalState != CardFailed {
		t.Errorf("final state = %v, want CardFailed (failed slot dominates)", m.root.finalState)
	}
}

// TestGroupModelSlotUnknownDropped pins the defensive branch: a
// slot-tagged child for a slot the model never saw is dropped rather
// than misplaced at the tail.
func TestGroupModelSlotUnknownDropped(t *testing.T) {
	m := newGroupModel("parent", 0, nil)
	m.processMsg(groupChildMsg{rendered: "GHOST\n", state: CardSuccess, slot: 99})
	if view := m.viewString(); strings.Contains(view, "GHOST") {
		t.Errorf("unknown-slot row rendered:\n%s", view)
	}
	if n := len(m.root.children); n != 0 {
		t.Errorf("unknown-slot row appended as child (%d children)", n)
	}
}

// TestFanOutSlots runs FanOut against a real group reporter with a
// scripted completion order (reverse of item order) and verifies the
// message stream reconstructs into rows in item order, all resolved.
func TestFanOutSlots(t *testing.T) {
	const n = 3
	ch := make(chan groupMsg, 64)
	g := newGroup(nil, "parent", 1, ch)

	gates := make([]chan struct{}, n)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	go func() {
		// Release in reverse so completion order inverts item order.
		for i := n - 1; i >= 0; i-- {
			close(gates[i])
		}
	}()

	FanOut(g, n, n,
		func(i int) string { return fmt.Sprintf("item-%d", i) },
		func(i int) { <-gates[i] },
		func(i int, r Reporter) { r.Complete(fmt.Sprintf("resolved-%d", i)) },
	)
	close(ch)

	m := newGroupModel("parent", 0, nil)
	for msg := range ch {
		m.processMsg(msg)
	}
	m.processMsg(groupDoneMsg{})

	view := m.viewString()
	last := -1
	for i := range n {
		at := strings.Index(view, fmt.Sprintf("Resolved-%d", i))
		if at < 0 {
			t.Fatalf("item %d never resolved:\n%s", i, view)
		}
		if at < last {
			t.Errorf("item %d resolved out of slot order:\n%s", i, view)
		}
		last = at
	}
	if strings.Contains(view, "Item-") {
		t.Errorf("a slot still renders its running row after FanOut returned:\n%s", view)
	}
	if got := g.aggregate(); got != CardSuccess {
		t.Errorf("aggregate = %v, want CardSuccess", got)
	}
}

// TestFanOutFallback pins the degraded path for reporters that can't
// host slots: work still completes for every item, and resolutions
// emit in item order so piped/captured output stays deterministic.
func TestFanOutFallback(t *testing.T) {
	capture := NewCaptureReporter()
	worked := make([]bool, 4)
	FanOut(capture, len(worked), 2,
		func(i int) string { return fmt.Sprintf("item-%d", i) },
		func(i int) { worked[i] = true },
		func(i int, r Reporter) { r.Complete(fmt.Sprintf("resolved-%d", i)) },
	)
	for i, w := range worked {
		if !w {
			t.Errorf("work(%d) never ran", i)
		}
	}
	events := capture.OfKind(CaptureComplete)
	if len(events) != len(worked) {
		t.Fatalf("%d resolutions captured, want %d\n%s", len(events), len(worked), capture.Dump())
	}
	for i, ev := range events {
		if want := fmt.Sprintf("resolved-%d", i); ev.Label != want {
			t.Errorf("event %d = %q, want %q (item order)", i, ev.Label, want)
		}
	}
}
