package ui

// PTY smoke driver for the session shell's interactive path — the
// one surface the capture-reporter harness structurally cannot reach.
// Gated behind BOSUN_PTY_SMOKE so normal test runs skip it.
//
// Run it under a real (sized) PTY and eyeball the transcript:
//
//	go test -c -o /tmp/ui.test ./internal/ui/
//	BOSUN_PTY_SMOKE=1 /tmp/ui.test -test.run TestSessionPTYSmoke
//
// A headless PTY (script(1) with no controlling terminal) reports a
// zero winsize and the renderer paints nothing — drive it from a
// terminal, or via a forkpty wrapper that sets TIOCSWINSZ first.

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestSessionPTYSmoke(t *testing.T) {
	if os.Getenv("BOSUN_PTY_SMOKE") == "" {
		t.Skip("set BOSUN_PTY_SMOKE=1 under a PTY to run")
	}
	SetDefault(NewCardReporter())
	BeginTimeline()
	SetContext("demo", "SMOKE-1", "session smoke")

	err := RunSession(func() error {
		// Static card.
		NewCard(CardSuccess, "issue").Subtitle("SMOKE-1 · session smoke test").Print()

		// Spinner that succeeds.
		_ = RunCard("fetching data", func() error { time.Sleep(400 * time.Millisecond); return nil })

		// Group with child spinners.
		RunGroup("workspace readiness", func(g Reporter) {
			for _, name := range []string{"alpha-repo", "beta-repo"} {
				_ = g.Spinner(PreserveCase(name), func() error {
					time.Sleep(300 * time.Millisecond)
					return nil
				})
				g.Complete(PreserveCase(name))
			}
			_ = g.Task("probing", func() error { time.Sleep(300 * time.Millisecond); return nil })
		})

		// Step sequence resolving into a successor card.
		steps := []CardStep{
			{Card: NewCard(CardRunning, "services").Muted("scanning alpha..."), Run: func() error { time.Sleep(350 * time.Millisecond); return nil }},
			{Card: NewCard(CardRunning, "services").Muted("scanning beta..."), Run: func() error { time.Sleep(350 * time.Millisecond); return nil }},
		}
		rewind, err := RunCardSteps(steps, func() *Card {
			return NewCard(CardSuccess, "services").Subtitle("2 services detected")
		})
		if err != nil {
			return err
		}
		_ = rewind // keep the successor

		// Rewindable header that gets dropped (prompt-and-replace shape).
		drop := NewCard(CardInput, "pick one").Tight().PrintRewindable()
		time.Sleep(500 * time.Millisecond)
		drop()
		NewCard(CardSuccess, "picked").Subtitle("the default").Print()

		// A failing spinner to check the failure frame.
		_ = RunCard("flaky probe", func() error { return errors.New("boom (expected)") })

		// Plan card through apply.
		plan := NewPlan()
		plan.Add(PlanCreate, "deploy", "env", "brave-falcon", "preview env")
		plan.Add(PlanNoChange, "adopt", "env", "old-env", "current")
		pc := NewPlanCard(plan)
		return pc.RunApply([]PlanAction{{Run: func() error { time.Sleep(400 * time.Millisecond); return nil }}})
	})
	if err != nil {
		t.Logf("worker err: %v", err)
	}
	EndTimeline()
}
