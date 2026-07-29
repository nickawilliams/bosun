package ui

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestStartStepWorkerRunsAllSteps locks the happy path: every step
// runs in order, each completion is reported, and the worker exits on
// its own (closing quit afterwards is a harmless no-op).
func TestStartStepWorkerRunsAllSteps(t *testing.T) {
	var order []int
	steps := []CardStep{
		{Card: NewCard(CardInfo, "one"), Run: func() error { order = append(order, 1); return nil }},
		{Card: NewCard(CardInfo, "two"), Run: func() error { order = append(order, 2); return nil }},
	}

	resultCh, quit := startStepWorker(steps)
	for i := 0; i < len(steps); i++ {
		select {
		case err := <-resultCh:
			if err != nil {
				t.Fatalf("step %d error: %v", i+1, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for step %d", i+1)
		}
	}
	close(quit)

	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("run order = %v, want [1 2]", order)
	}
}

// TestStartStepWorkerQuitStopsBetweenSteps locks the interrupt
// contract: closing quit while a step is in flight lets that step
// finish but runs no further step's side effects, and the worker
// goroutine exits instead of blocking forever on the result send
// (regression: ctrl+c kept executing the remaining steps, then leaked
// the goroutine).
func TestStartStepWorkerQuitStopsBetweenSteps(t *testing.T) {
	step1Started := make(chan struct{})
	release1 := make(chan struct{})
	var ran2 atomic.Bool
	steps := []CardStep{
		{Card: NewCard(CardInfo, "one"), Run: func() error {
			close(step1Started)
			<-release1
			return nil
		}},
		{Card: NewCard(CardInfo, "two"), Run: func() error {
			ran2.Store(true)
			return nil
		}},
	}

	resultCh, quit := startStepWorker(steps)
	<-step1Started
	close(quit)     // the interrupt lands mid-step
	close(release1) // the in-flight step finishes

	// The worker either reports step 1's result or exits straight via
	// quit — both are fine; what must never happen is step 2 running.
	select {
	case <-resultCh:
	case <-time.After(3 * time.Second):
	}
	time.Sleep(100 * time.Millisecond) // let a (wrongly) scheduled step 2 surface
	if ran2.Load() {
		t.Fatal("step 2 ran after quit was closed")
	}
}

// stepsModel drives cardStepsModel directly — the tea program itself
// needs a TTY, but the model's Update/View are pure and carry all the
// lifecycle logic worth locking.
func stepsModel(successor func() *Card, rawSuccessor func() string, n int) (cardStepsModel, []*Card) {
	cards := make([]*Card, n)
	steps := make([]CardStep, n)
	for i := range steps {
		cards[i] = NewCard(CardRunning, "step")
		steps[i] = CardStep{Card: cards[i], Run: func() error { return nil }}
	}
	m := newCardStepsModel(steps, successor, " ", make(chan error))
	m.rawSuccessor = rawSuccessor
	return m, cards
}

// TestCardStepsModelAdvancesToSuccessorFrame locks the happy-path
// lifecycle: each step-done message advances the index, and the final
// frame after the last step is the successor's render (the takeover
// contract the rewind math depends on).
func TestCardStepsModelAdvancesToSuccessorFrame(t *testing.T) {
	final := NewCard(CardSuccess, "done")
	m, _ := stepsModel(func() *Card { return final }, nil, 2)

	next, _ := m.Update(cardStepDoneMsg{})
	m = next.(cardStepsModel)
	if m.done || m.idx != 1 {
		t.Fatalf("after first step: done=%v idx=%d, want running at idx 1", m.done, m.idx)
	}

	next, _ = m.Update(cardStepDoneMsg{})
	m = next.(cardStepsModel)
	if !m.done || m.err != nil {
		t.Fatalf("after last step: done=%v err=%v, want clean finish", m.done, m.err)
	}
	if got, want := m.View().Content, " "+final.Render(); got != want {
		t.Errorf("final frame = %q, want the successor render %q", got, want)
	}
}

// TestCardStepsModelFailureFrame locks the failure shape: the failed
// step's card flips to CardFailed with the error as subtitle and IS
// the permanent final frame.
func TestCardStepsModelFailureFrame(t *testing.T) {
	m, cards := stepsModel(nil, nil, 2)

	next, _ := m.Update(cardStepDoneMsg{err: errors.New("boom")})
	m = next.(cardStepsModel)
	if !m.done || m.err == nil {
		t.Fatalf("done=%v err=%v, want failed finish", m.done, m.err)
	}
	if cards[0].state != CardFailed {
		t.Errorf("failed step card state = %v, want CardFailed", cards[0].state)
	}
	if got := m.View().Content; !strings.Contains(got, "boom") {
		t.Errorf("final frame missing the error subtitle: %q", got)
	}
}

// TestCardStepsModelInterruptFrame locks ctrl+c handling: the model
// finishes with an interrupted error and the IN-FLIGHT card leaves
// CardRunning (regression: the final frame showed a forever-running
// glyph above the cancellation output).
func TestCardStepsModelInterruptFrame(t *testing.T) {
	m, cards := stepsModel(nil, nil, 2)

	// First step completes; the second is in flight when ctrl+c lands.
	next, _ := m.Update(cardStepDoneMsg{})
	m = next.(cardStepsModel)

	next, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(cardStepsModel)
	if !m.done || m.err == nil || m.err.Error() != "interrupted" {
		t.Fatalf("done=%v err=%v, want interrupted", m.done, m.err)
	}
	if cards[1].state != CardFailed {
		t.Errorf("in-flight card state = %v, want CardFailed (not forever-running)", cards[1].state)
	}
	if got := m.View().Content; !strings.Contains(got, "interrupted") {
		t.Errorf("final frame missing the interrupted subtitle: %q", got)
	}
}

// TestCardStepsModelRawSuccessorFrame locks RunCardStepsInto's
// takeover contract: on success the final frame is finalView()'s
// string verbatim, so the next program's first repaint is
// byte-identical.
func TestCardStepsModelRawSuccessorFrame(t *testing.T) {
	const frame = "EXACT NEXT PROGRAM FRAME\nline two\n"
	m, _ := stepsModel(nil, func() string { return frame }, 1)

	next, _ := m.Update(cardStepDoneMsg{})
	m = next.(cardStepsModel)
	if got, want := m.View().Content, " "+frame; got != want {
		t.Errorf("raw final frame = %q, want %q verbatim", got, want)
	}
}
