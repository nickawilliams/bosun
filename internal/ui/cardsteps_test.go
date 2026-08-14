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

// TestRunCardStepsRawPathEmitsSuccessor locks the fix for issue #72:
// in raw mode the successor card is routed through EmitToReporter so
// that plainReporter can emit a plain-text line while rawReporter
// stays silent. We install a CaptureReporter (which IsRaw counts as
// raw) and verify a CardSuccess successor produces a CaptureComplete
// event.
func TestRunCardStepsRawPathEmitsSuccessor(t *testing.T) {
	old := Default()
	cap := NewCaptureReporter()
	SetDefault(cap)
	t.Cleanup(func() { SetDefault(old) })

	steps := []CardStep{
		{
			Card: NewCard(CardInfo, "detect"),
			Run:  func() error { return nil },
		},
	}
	successor := func() *Card {
		return NewCard(CardSuccess, "preview").Subtitle("prod-env")
	}

	rewind, err := RunCardSteps(steps, successor)
	if err != nil {
		t.Fatalf("RunCardSteps returned error: %v", err)
	}
	if rewind == nil {
		t.Fatal("RunCardSteps returned nil rewind")
	}

	evts := cap.Events()
	if len(evts) != 1 {
		t.Fatalf("got %d events, want 1 for the successor: %s", len(evts), cap.Dump())
	}
	e := evts[0]
	if e.Kind != CaptureComplete {
		t.Errorf("Kind = %q, want %q", e.Kind, CaptureComplete)
	}
	if e.Label != "preview" {
		t.Errorf("Label = %q, want %q", e.Label, "preview")
	}
	if e.Value != "prod-env" {
		t.Errorf("Value = %q, want %q", e.Value, "prod-env")
	}
}

// TestRunCardStepsRawPathSilentForInputSuccessor locks that CardInput
// successors stay silent in raw mode: form headers are only meaningful
// in interactive TTY mode, so emitting them to a plain reporter would
// be noise.
func TestRunCardStepsRawPathSilentForInputSuccessor(t *testing.T) {
	old := Default()
	cap := NewCaptureReporter()
	SetDefault(cap)
	t.Cleanup(func() { SetDefault(old) })

	steps := []CardStep{
		{Card: NewCard(CardInfo, "step"), Run: func() error { return nil }},
	}
	successor := func() *Card { return NewCard(CardInput, "select workspace") }

	if _, err := RunCardSteps(steps, successor); err != nil {
		t.Fatalf("RunCardSteps returned error: %v", err)
	}
	if evts := cap.Events(); len(evts) != 0 {
		t.Errorf("expected no events for CardInput successor, got %d: %s",
			len(evts), cap.Dump())
	}
}

// TestRunCardStepsIntoRawPathCallsFinalView locks the fix for issue
// #72: in raw mode RunCardStepsInto invokes the finalView closure for
// its side effects even though no ANSI is painted. The callers' post-
// call branches (applyDefaults, formGate state) rely on these side
// effects.
func TestRunCardStepsIntoRawPathCallsFinalView(t *testing.T) {
	old := Default()
	SetDefault(NewCaptureReporter())
	t.Cleanup(func() { SetDefault(old) })

	var sideEffectRan bool
	steps := []CardStep{
		{Card: NewCard(CardInfo, "step"), Run: func() error { return nil }},
	}
	finalView := func() string {
		sideEffectRan = true
		return "some ANSI frame\n"
	}

	if err := RunCardStepsInto(steps, finalView); err != nil {
		t.Fatalf("RunCardStepsInto returned error: %v", err)
	}
	if !sideEffectRan {
		t.Error("finalView was not called in raw mode (side effects skipped)")
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
