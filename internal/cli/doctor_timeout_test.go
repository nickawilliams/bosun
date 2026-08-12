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
//
// The probe budget covers only the network portion of a check, and
// TestProbeBudgetExcludesServiceResolution is the reason why: an
// earlier revision timed the whole check body, which charged config
// prompting on a TTY against the integration and reported a healthy
// tracker as unreachable.

// withDoctorTimeouts swaps both budgets for the duration of a test so
// the assertions run in milliseconds rather than the production 30s.
func withDoctorTimeouts(t *testing.T, run, probe time.Duration) {
	t.Helper()

	origRun, origProbe := doctorRunTimeout, doctorProbeTimeout
	doctorRunTimeout, doctorProbeTimeout = run, probe
	t.Cleanup(func() {
		doctorRunTimeout, doctorProbeTimeout = origRun, origProbe
	})
}

// blockUntilDone is the in-process stand-in for a host that accepts a
// connection and never answers.
func blockUntilDone(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// A wedged probe must cost its own slice of the budget, not all of it.
func TestProbeBoundsEachCheckSeparately(t *testing.T) {
	withDoctorTimeouts(t, 2*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	start := time.Now()
	probeCtx, cancelProbe := probeContext(ctx)
	defer cancelProbe()
	err := blockUntilDone(probeCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	// Before the probe context existed the call ran until the run
	// budget expired. That is the regression this guards.
	if elapsed >= doctorRunTimeout {
		t.Errorf("probe ran %v, consuming the whole %v run budget; want ~%v",
			elapsed, doctorRunTimeout, doctorProbeTimeout)
	}
	if elapsed < doctorProbeTimeout {
		t.Errorf("probe returned after %v, before its %v budget elapsed", elapsed, doctorProbeTimeout)
	}
}

// The point of bounding one probe is that the next check still gets to
// run and report a real answer.
func TestProbeLeavesBudgetForLaterChecks(t *testing.T) {
	withDoctorTimeouts(t, 2*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	// Three wedged integrations, the shape of a doctor run against a
	// host that is down for all of them.
	for range 3 {
		probeCtx, cancelProbe := probeContext(ctx)
		_ = blockUntilDone(probeCtx)
		cancelProbe()
	}

	// A healthy check running after them must still reach the network
	// rather than inherit an exhausted context.
	probeCtx, cancelProbe := probeContext(ctx)
	defer cancelProbe()
	if err := probeCtx.Err(); err != nil {
		t.Fatalf("probe after three wedged ones was already expired: %v", err)
	}
}

// Service resolution — reading config, shelling out to `gh`, and on a
// TTY prompting for missing keys — must not be charged to the probe
// budget. An earlier revision timed the whole check body, so a user
// who took longer than the budget to answer a prompt got
// "connection failed: context deadline exceeded" for an integration
// that was never contacted.
func TestProbeBudgetExcludesServiceResolution(t *testing.T) {
	withDoctorTimeouts(t, 2*time.Second, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	// Stand in for newIssueTracker() prompting the user, taking well
	// over the probe budget to return.
	time.Sleep(3 * doctorProbeTimeout)

	// The probe that follows must still get its full budget against a
	// reachable service.
	probeCtx, cancelProbe := probeContext(ctx)
	defer cancelProbe()

	if err := probeCtx.Err(); err != nil {
		t.Fatalf("probe context was already expired after service resolution: %v; "+
			"a healthy integration would be reported as unreachable", err)
	}

	// And a service that answers promptly reports success, not a
	// deadline error.
	if err := reachableProbe(probeCtx); err != nil {
		t.Errorf("probe against a reachable service failed: %v", err)
	}
}

// reachableProbe models a service that answers well inside the budget.
func reachableProbe(ctx context.Context) error {
	select {
	case <-time.After(5 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The probe budget must not become an escape hatch that outlives the
// run budget — the run timeout is the caller's contract and stays the
// hard cap.
func TestProbeCannotOutlastRunBudget(t *testing.T) {
	withDoctorTimeouts(t, 100*time.Millisecond, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), doctorRunTimeout)
	defer cancel()

	start := time.Now()
	probeCtx, cancelProbe := probeContext(ctx)
	defer cancelProbe()
	err := blockUntilDone(probeCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	// Tight enough to catch a probe parented to Background rather than
	// to the run context, which would run for the full 10s.
	if elapsed > 10*doctorRunTimeout {
		t.Errorf("probe ran %v, outlasting the %v run budget under a longer probe budget",
			elapsed, doctorRunTimeout)
	}
}

// A probe started after the run budget is already gone must fail
// immediately rather than be granted a fresh budget.
func TestProbeOnExhaustedRunBudgetFailsImmediately(t *testing.T) {
	withDoctorTimeouts(t, time.Second, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	probeCtx, cancelProbe := probeContext(ctx)
	defer cancelProbe()
	err := blockUntilDone(probeCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("probe took %v on an already-cancelled context; want an immediate return", elapsed)
	}
}
