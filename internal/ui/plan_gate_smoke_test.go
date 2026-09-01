package ui

// PTY smoke driver for the plan confirmation gate — the exact
// header/form/rewind/apply sequence runPlanCard drives, which is where
// issue #94's renderer regression surfaced: after approving, the tail
// shrinks from the tall form frame to the shorter applying card, and a
// broken inline renderer strands the old frame's top rows in
// scrollback (an orphaned connector row, or worse the pending header
// and plan items, fossilized above the Success card).
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
// Healthy output has exactly one blank connector row between the
// Repositories card and the Success card, and no Pending header left
// behind.

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

		// runPlanCard's interactive gate, verbatim shape.
		rewind := NewCard(CardInput, "Pending").Value(plan.Summary()).Tight().PrintRewindable()

		var confirmed bool
		form := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().
				Title(plan.RenderItems()).
				Affirmative("Approve").
				Negative("Cancel").
				Value(&confirmed),
		)).WithWidth(TermWidth())

		if err := SessionForm(form, false); err != nil {
			return err
		}

		rewind()

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
