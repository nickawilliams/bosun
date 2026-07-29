package ui

import (
	"sync/atomic"
	"testing"
	"time"
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
