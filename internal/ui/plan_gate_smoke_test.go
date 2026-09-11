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
// Healthy output has exactly ONE plan card in scrollback — the
// Success (or Cancelled) card in the position the Pending prompt
// occupied — with no Pending header and no confirm buttons left
// behind, and one blank connector row above it.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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

		// The plan to confirm and apply: 2 creates + 1 modify, or
		// BOSUN_SMOKE_ROWS filler rows to drive the oversized-plan
		// fallback.
		plan := NewPlan()
		if n, _ := strconv.Atoi(os.Getenv("BOSUN_SMOKE_ROWS")); n > 0 {
			for i := range n {
				plan.Add(PlanCreate, "add", "worktree", "host-ui", fmt.Sprintf("feature/EX-%d_slug", i))
			}
		} else {
			plan.Add(PlanCreate, "add", "branch", "host-ui", "feature/EX-1_slug")
			plan.Add(PlanCreate, "add", "worktree", "host-ui", "_workspaces/feature")
			plan.Add(PlanModify, "move", "status", "EX-1", "Backlog → In Progress")
		}
		pc := NewPlanCard(plan)

		// runPlanCard's interactive gate, verbatim shape: the plan
		// card IS the prompt, rewindable so every stage cycles in
		// its one position, with only the bare Approve/Cancel
		// buttons live beneath it (#120). A plan too tall for the
		// live frame commits its rows first and cycles compactly —
		// planGateFits' bound, mirrored here (the constant itself
		// lives in the cli layer with the form plumbing).
		lines := strings.Count(strings.TrimSuffix(pc.Render(), "\n"), "\n") + 1
		if lines+5 > TermHeight() {
			plan.Print()
			pc.Compact()
		}
		rewind := pc.PrintRewindable()

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

		// Either answer cycles the card in place (runPlanCard's
		// shape).
		rewind()

		if !confirmed {
			pc.SetState(PlanCancelled)
			pc.Print()
			return nil
		}

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
