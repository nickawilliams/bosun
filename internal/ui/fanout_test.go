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
	// the group finalizes to the worst child outcome. The extra
	// skip/info rows pin the remaining count arms for slot-tagged
	// children.
	m.processMsg(groupChildMsg{rendered: "ROW-ALPHA\n", state: CardFailed, slot: 11})
	m.processMsg(groupChildMsg{rendered: "ROW-ALPHA-WARN\n", state: CardSkipped, slot: 11})
	m.processMsg(groupChildMsg{rendered: "ROW-ALPHA-INFO\n", state: CardInfo, slot: 11})
	m.processMsg(groupSlotDoneMsg{slot: 11})
	m.processMsg(groupDoneMsg{})

	if c := m.root.counts; c.failed != 1 || c.skipped != 1 || c.info != 1 || c.success != 1 {
		t.Errorf("counts = %+v, want one of each state", c)
	}

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

// TestGroupBareIndent pins the successor-flow indent contract: a
// bare group's rows — resolved children and running slot rows alike
// — indent with whitespace only, no timeline spine. The transient
// render never joins the timeline; the spine belongs to the
// committed record that replaces it.
func TestGroupBareIndent(t *testing.T) {
	ch := make(chan groupMsg, 16)
	g := newGroup(nil, "parent", 1, ch)
	g.bare = true
	g.Complete("bare-child")
	close(ch)

	m := newGroupModel("parent", 0, nil)
	m.bare = true
	for msg := range ch {
		m.processMsg(msg)
	}
	m.processMsg(groupSlotStartMsg{title: "running-slot", indent: 1, slot: 21})

	view := m.viewString()
	if !strings.Contains(view, "Bare-child") || !strings.Contains(view, "Running-slot") {
		t.Fatalf("view missing rows:\n%s", view)
	}
	if strings.Contains(view, cardConnector) {
		t.Errorf("bare group rendered the timeline spine:\n%s", view)
	}

	// The default (spined) mode is unchanged.
	ch2 := make(chan groupMsg, 16)
	g2 := newGroup(nil, "parent", 1, ch2)
	g2.Complete("spined-child")
	close(ch2)
	m2 := newGroupModel("parent", 0, nil)
	for msg := range ch2 {
		m2.processMsg(msg)
	}
	if view := m2.viewString(); !strings.Contains(view, cardConnector) {
		t.Errorf("classic group lost its spine:\n%s", view)
	}
}

// TestGroupModelStatus pins the status caption: each Status message
// overwrites the last, the caption renders under the title (with a
// trailing blank once child rows exist, so they don't crowd it),
// and the last value set survives onto the finalized render.
func TestGroupModelStatus(t *testing.T) {
	m := newGroupModel("parent", 0, nil)
	m.processMsg(groupStatusMsg{text: "resolving..."})
	if view := m.viewString(); !strings.Contains(view, "resolving...") {
		t.Errorf("view = %q, want the status caption", view)
	}

	m.processMsg(groupStatusMsg{text: "3 found, checking..."})
	m.processMsg(groupChildMsg{rendered: "ROW\n", state: CardSuccess})
	view := m.viewString()
	if strings.Contains(view, "resolving...") {
		t.Errorf("view = %q, stale caption survived an overwrite", view)
	}
	statusAt := strings.Index(view, "3 found, checking...")
	rowAt := strings.Index(view, "ROW")
	if statusAt < 0 || rowAt < 0 || statusAt > rowAt {
		t.Errorf("caption not rendered above the rows (status %d, row %d):\n%s", statusAt, rowAt, view)
	}

	m.processMsg(groupDoneMsg{})
	if view := m.viewString(); !strings.Contains(view, "3 found, checking...") {
		t.Errorf("finalized view = %q, want the last caption kept", view)
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

// TestFanOutNothingToDo pins the empty-input no-ops: neither helper
// may spin up machinery (or panic on empty slices) for zero items.
func TestFanOutNothingToDo(t *testing.T) {
	called := false
	FanOut(NewCaptureReporter(), 0, 0,
		func(int) string { return "" },
		func(int) { called = true },
		func(int, Reporter) { called = true },
	)
	RunBounded(0, 0, func(int) { called = true })
	if called {
		t.Error("zero items still invoked work or resolve")
	}
}

// TestGroupWorkerSideCounts pins the worker-side aggregate for the
// paths the model doesn't drive: a failed Task and a nested sub-group
// both tally through bumpCount into the shared counts.
func TestGroupWorkerSideCounts(t *testing.T) {
	ch := make(chan groupMsg, 64)
	g := newGroup(nil, "parent", 1, ch)

	if err := g.Task("boom", func() error { return fmt.Errorf("boom") }); err == nil {
		t.Fatal("Task swallowed its error")
	}
	g.Group("inner", func(inner Reporter) {
		inner.Complete("child")
	})

	if got := g.aggregate(); got != CardFailed {
		t.Errorf("aggregate = %v, want CardFailed (failed Task dominates the sub-group success)", got)
	}
}

// TestRunGroupThenFallback pins the non-card-reporter contract: the
// group still runs (emitting through the reporter as usual), the
// successor is never rendered, and the rewind comes back nil —
// callers mount their own replacement.
func TestRunGroupThenFallback(t *testing.T) {
	capture := NewCaptureReporter()
	old := Default()
	SetDefault(capture)
	t.Cleanup(func() { SetDefault(old) })

	ran := false
	rewind := RunGroupThen("readiness", func(g Reporter) {
		ran = true
		g.Complete("child")
	}, func() *Card { return NewCard(CardInput, "picker").Tight() })
	if !ran {
		t.Error("group callback never ran")
	}
	if rewind != nil {
		t.Error("capture reporter returned a rewind; only card renders can mount the successor")
	}
	if _, ok := capture.Find("child"); !ok {
		t.Errorf("group child not captured\n%s", capture.Dump())
	}
}

// TestRunGroupThenHeadlessFallback drives the standalone card
// reporter's group against buffer streams, where the BubbleTea
// program can't run: the group must degrade to the drain fallback
// (children printed, no live render), skip the successor, and
// return a nil rewind — the non-TTY fallback prints straight to
// scrollback, so there is nothing to rewind or replace. The TTY
// success path (successor swap) is exercised by the gated
// TestGroupSlotsPTYSmoke.
func TestRunGroupThenHeadlessFallback(t *testing.T) {
	sessionTestStreams(t) // card reporter + buffer streams, no session

	ran := false
	rewind := RunGroupThen("headless group", func(g Reporter) {
		ran = true
		g.Complete("child")
	}, func() *Card { return NewCard(CardInput, "picker").Tight() })
	if !ran {
		t.Error("group callback never ran")
	}
	if rewind != nil {
		t.Error("headless fallback returned a rewind; its output went straight to scrollback")
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
