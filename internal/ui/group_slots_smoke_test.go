package ui

// PTY smoke driver for the fan-out group → picker sequence bulk
// cleanup drives, sized so the group block is tall relative to the
// terminal — the regime where committing it used to fossilize a
// stale duplicate of the frame's top rows into scrollback (the #120
// double "Cleanup Readiness" report; see commitOpen's settle).
// Gated behind BOSUN_PTY_SMOKE. Drive via tmux:
//
//	go test -c -o /tmp/ui.test ./internal/ui/
//	tmux new-session -d -s slotsmoke -x 200 -y 50
//	tmux send-keys -t slotsmoke 'BOSUN_PTY_SMOKE=1 /tmp/ui.test -test.run TestGroupSlotsPTYSmoke' Enter
//	sleep 8; tmux send-keys -t slotsmoke Enter; sleep 2
//	tmux capture-pane -t slotsmoke -p -S -200
//
// Healthy output has exactly ONE "Cleanup Readiness" header in the
// pane history: the group card morphs into the picker (rewound by
// RunGroupRewindable), and after submit the record card — same
// title, selection folded in — is the only copy that persists.
// BOSUN_SMOKE_N overrides the workspace count (default 25 — tall
// enough that 2×frame exceeds a 50-row terminal).
//
// Remaining known limitation, deliberately out of this driver's
// scope: a live group frame TALLER than the terminal itself (e.g.
// N=25 on a 28-row pane) still strands rows — that is the
// frame-taller-than-screen regime (#69/#98 family), which needs
// frame capping, not commit ordering.

import (
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"charm.land/huh/v2"
)

func TestGroupSlotsPTYSmoke(t *testing.T) {
	if os.Getenv("BOSUN_PTY_SMOKE") == "" {
		t.Skip("set BOSUN_PTY_SMOKE=1 under a PTY to run")
	}
	SetDefault(NewCardReporter())
	BeginTimeline()

	nChildren := 25
	if v := os.Getenv("BOSUN_SMOKE_N"); v != "" {
		nChildren, _ = strconv.Atoi(v)
	}

	err := RunSession(func() error {
		// Mirror runCleanupBulk's shape: a RunCardThen spinner that
		// resolves to the standing Resolving card, then the fan-out
		// group, then the picker form.
		_ = RunCardThen("Resolving Workspaces", func() error {
			time.Sleep(400 * time.Millisecond)
			return nil
		}, func() *Card {
			return NewCard(CardSuccess, "Resolving Workspaces").Value(fmt.Sprintf("%d workspaces found", nChildren))
		})

		// The pickBulkCandidates morph: the group finalizes into the
		// picker's input header (no empty-frame flash between them),
		// the form mounts beneath it, and one record card replaces
		// both on submit.
		rewind := RunGroupThen("cleanup readiness", func(grp Reporter) {
			FanOut(grp, nChildren, 0,
				func(i int) string { return fmt.Sprintf("ws-%d", i) },
				func(i int) { time.Sleep(time.Duration(200+(i%7)*120) * time.Millisecond) },
				func(i int, slot Reporter) {
					if i%5 == 2 {
						slot.FailValue(fmt.Sprintf("ws-%d", i), "blocked (re-run with --force to select)")
						return
					}
					slot.Complete(fmt.Sprintf("ws-%d", i))
				},
			)
		}, func() *Card { return NewCard(CardInput, "select workspaces").Tight() })

		opts := make([]huh.Option[int], nChildren)
		for i := range opts {
			opts[i] = huh.NewOption(fmt.Sprintf("ws-%d", i), i).Selected(i%5 != 2)
		}
		var picked []int
		form := huh.NewForm(huh.NewGroup(
			huh.NewMultiSelect[int]().Options(opts...).Value(&picked),
		)).WithWidth(TermWidth())
		if err := SessionForm(form, false); err != nil {
			return err
		}
		if rewind != nil {
			rewind()
		}
		record := NewCard(CardSuccess, "cleanup readiness").
			Value(fmt.Sprintf("%d of %d selected", len(picked), nChildren))
		for i := range nChildren {
			record.Muted(fmt.Sprintf("ws-%d", i))
		}
		record.Print()
		// A successor block, so the tall record goes through
		// commitOpen's clear-and-settle arm (the fossil guard) inside
		// this same PTY run — in the real flow the assessing spinner
		// plays this role.
		NewCard(CardSuccess, "assessing stand-in").Print()
		return nil
	})
	if err != nil {
		t.Logf("worker err: %v", err)
	}
	EndTimeline()
}
