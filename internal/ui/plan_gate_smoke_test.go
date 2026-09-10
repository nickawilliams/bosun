package ui

// PTY smoke driver for the plan confirmation gate — the exact
// committed-card/form/apply sequence runPlanCard drives, which is
// where issue #94's renderer regression surfaced: after approving,
// the tail swaps from the form frame to the applying card, and a
// broken inline renderer strands the old frame's rows in scrollback
// (an orphaned connector row, or worse the confirm buttons
// fossilized above the Success card).
//
// Gated behind BOSUN_PTY_SMOKE like TestSessionPTYSmoke. Drive it from
// a real terminal and approve the plan, or via tmux:
//
//	go test -c -o /tmp/ui.test ./internal/ui/
//	tmux new-session -d -s smoke -x 120 -y 50
//	tmux send-keys -t smoke 'BOSUN_PTY_SMOKE=1 /tmp/ui.test -test.run TestPlanGatePTYSmoke' Enter
//	sleep 4; tmux send-keys -t smoke Enter; sleep 3
//	tmux capture-pane -t smoke -p
//
// Healthy output has the Pending card (with its rows) standing above
// the Success card, one blank connector row between blocks, and no
// confirm buttons left behind.

import (
	"os"
	"testing"

	"charm.land/huh/v2"
)

func TestPlanGatePTYSmoke(t *testing.T) {
	if os.Getenv("BOSUN_PTY_SMOKE") == "" {
		t.Skip("set BOSUN_PTY_SMOKE=1 under a PTY to run")
	}
	SetDefault(NewCardReporter())
	BeginTimeline()

	err := RunSession(func() error {
		// Stand-in for the record card above the gate.
		NewCard(CardSuccess, "repositories").Value("host-ui").Print()

		// The plan to confirm and apply: 2 creates + 1 modify.
		plan := NewPlan()
		plan.Add(PlanCreate, "add", "branch", "host-ui", "feature/EX-1_slug")
		plan.Add(PlanCreate, "add", "worktree", "host-ui", "_workspaces/feature")
		plan.Add(PlanModify, "move", "status", "EX-1", "Backlog → In Progress")
		pc := NewPlanCard(plan)

		// runPlanCard's interactive gate, verbatim shape: one Pending
		// card carrying the plan rows committed straight to
		// scrollback, with only the bare Approve/Cancel buttons live
		// beneath it (#120 — the plan never enters the live frame, so
		// a data-scaled plan can't oversize it).
		pc.PrintCommitted()

		var confirmed bool
		form := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Affirmative("Approve").
				Negative("Cancel").
				Value(&confirmed),
		)).WithTheme(FormTheme()).WithWidth(TermWidth())

		if err := SessionForm(form, false); err != nil {
			return err
		}

		// Post-approve the cycle continues compactly — the Pending
		// record above carries the rows (runPlanCard's shape).
		pc.Compact()
		return pc.RunApply([]PlanAction{
			{Run: func() error { return nil }},
			{Run: func() error { return nil }},
			{Run: func() error { return nil }},
		})
	})
	if err != nil {
		t.Logf("worker err: %v", err)
	}
	EndTimeline()
}
