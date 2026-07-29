package ui

import "testing"

// TestGroupTaskDoneOrdering locks the message-ordering fix: by the
// time Task/Spinner unblocks its caller, the groupTaskDoneMsg is
// already on msgCh. Without that guarantee the caller's next
// Task/Spinner could push its start message first, and the stale done
// message then cleared the NEW task's spinner row — or silently
// dropped a later task's result card via the activeTask guard.
func TestGroupTaskDoneOrdering(t *testing.T) {
	drain := func(ch chan groupMsg) (starts, dones int) {
		for {
			select {
			case msg := <-ch:
				switch msg.(type) {
				case groupTaskStartMsg:
					starts++
				case groupTaskDoneMsg:
					dones++
				}
			default:
				return
			}
		}
	}

	t.Run("Spinner", func(t *testing.T) {
		ch := make(chan groupMsg, 8)
		g := &group{msgCh: chan<- groupMsg(ch)}
		if err := g.Spinner("probe", func() error { return nil }); err != nil {
			t.Fatalf("Spinner: %v", err)
		}
		starts, dones := drain(ch)
		if starts != 1 || dones != 1 {
			t.Fatalf("msgCh after Spinner returned: %d starts / %d dones, want 1/1 — the done message must land before the caller unblocks", starts, dones)
		}
	})

	t.Run("Task", func(t *testing.T) {
		ch := make(chan groupMsg, 8)
		g := &group{msgCh: chan<- groupMsg(ch)}
		if err := g.Task("probe", func() error { return nil }); err != nil {
			t.Fatalf("Task: %v", err)
		}
		starts, dones := drain(ch)
		if starts != 1 || dones != 1 {
			t.Fatalf("msgCh after Task returned: %d starts / %d dones, want 1/1 — the done message must land before the caller unblocks", starts, dones)
		}
		if g.counts.success != 1 {
			t.Errorf("success count = %d, want 1", g.counts.success)
		}
	})
}
