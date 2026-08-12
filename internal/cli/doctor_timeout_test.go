package cli

import (
	"context"
	"errors"
	"testing"
	"time"
)

// doctor's budget has two levels and these pin what each one buys.
//
// Issue #54 reported a doctor run overrunning its 30s check context
// against an unreachable Jira host. Measured against the adapter, the
// run does stop at 30s — the deadline was already honored — but a
// single wedged integration spent the whole 30s in its dial, leaving
// nothing for the code host, notification, and CI/CD checks that ran
// after it. A diagnostic that reports four broken integrations when
// one is broken is the failure worth fixing.

// withDoctorTimeouts swaps both budgets for the duration of a test so
// the assertions run in milliseconds rather than the production 30s.
func withDoctorTimeouts(t *testing.T, run, check time.Duration) {
	t.Helper()

	origRun, origCheck := doctorRunTimeout, doctorCheckTimeout
	doctorRunTimeout, doctorCheckTimeout = run, check
	t.Cleanup(func() {
		doctorRunTimeout, doctorCheckTimeout = origRun, origCheck
	})
}

// blockingCheck returns a check that blocks until its context is done
// — the in-process stand-in for a host that accepts a connection and
// never answers.
func blockingCheck(observed *time.Duration) healthCheck {
	return healthCheck{
		Name: "wedged",
		Check: func(ctx context.Context) (string, error) {
			start := time.Now()
			<-ctx.Done()
			*observed = time.Since(start)
			return "", ctx.Err()
		},
	}
}

// A wedged check must cost its own slice of the budget, not all of it.
func TestRunCheckBoundsEachCheckSeparately(t *testing.T) {
	withDoctorTimeouts(t, 2*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	var observed time.Duration
	var detail string

	start := time.Now()
	err := runCheck(ctx, blockingCheck(&observed), &detail)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	// The check must be cut off at its own budget. Before the per-check
	// context existed it ran until the run budget expired, which is the
	// regression this guards.
	if elapsed >= doctorRunTimeout {
		t.Errorf("check ran %v, consuming the whole %v run budget; want ~%v",
			elapsed, doctorRunTimeout, doctorCheckTimeout)
	}
	if elapsed < doctorCheckTimeout {
		t.Errorf("check returned after %v, before its %v budget elapsed", elapsed, doctorCheckTimeout)
	}
}

// The point of bounding one check is that the next one still gets to
// run and report a real answer.
func TestRunCheckLeavesBudgetForLaterChecks(t *testing.T) {
	withDoctorTimeouts(t, 2*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	var observed time.Duration
	var detail string

	// Three wedged integrations, the shape of a doctor run against a
	// host that is down for all of them.
	for range 3 {
		_ = runCheck(ctx, blockingCheck(&observed), &detail)
	}

	// A healthy check running after them must still succeed rather
	// than inherit an exhausted context.
	healthy := healthCheck{
		Name: "code host",
		Check: func(ctx context.Context) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return "github → octocat", nil
		},
	}

	if err := runCheck(ctx, healthy, &detail); err != nil {
		t.Fatalf("healthy check after three wedged ones failed: %v", err)
	}
	if detail != "github → octocat" {
		t.Errorf("detail = %q, want the healthy check's own result", detail)
	}
}

// The per-check budget must not become an escape hatch that outlives
// the run budget — the run timeout is the caller's contract and stays
// the hard cap.
func TestRunCheckCannotOutlastRunBudget(t *testing.T) {
	withDoctorTimeouts(t, 100*time.Millisecond, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	var observed time.Duration
	var detail string

	start := time.Now()
	err := runCheck(ctx, blockingCheck(&observed), &detail)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= doctorCheckTimeout {
		t.Errorf("check ran %v, outlasting the %v run budget under a longer per-check budget",
			elapsed, doctorRunTimeout)
	}
}

// A check started after the run budget is already gone must fail
// immediately rather than be granted a fresh per-check budget.
func TestRunCheckOnExhaustedRunBudgetFailsImmediately(t *testing.T) {
	withDoctorTimeouts(t, time.Second, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var observed time.Duration
	var detail string

	start := time.Now()
	err := runCheck(ctx, blockingCheck(&observed), &detail)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("check took %v on an already-cancelled context; want an immediate return", elapsed)
	}
}
