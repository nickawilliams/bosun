package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// readyProvider is fakePreviewProvider with a scriptable Ready. The
// resolution fake answers nil for every operation, which is what the
// name-matrix tests want; readiness is the thing under test here.
type readyProvider struct {
	fakePreviewProvider
	errs map[preview.Operation]error
	ops  []preview.Operation
}

func (r *readyProvider) Ready(_ context.Context, op preview.Operation) error {
	r.ops = append(r.ops, op)
	return r.errs[op]
}

// captureUI installs a CaptureReporter as the default for the test and
// returns it, so ui.Skip calls are assertable.
func captureUI(t *testing.T) *ui.CaptureReporter {
	t.Helper()
	prev := ui.Default()
	rep := ui.NewCaptureReporter()
	ui.SetDefault(rep)
	t.Cleanup(func() { ui.SetDefault(prev) })
	return rep
}

// skipLabels returns every label ui.Skip was called with.
func skipLabels(rep *ui.CaptureReporter) []string {
	var out []string
	for _, ev := range rep.OfKind(ui.CaptureSkip) {
		out = append(out, ev.Label)
	}
	return out
}

func TestPreviewReadiness_ReadyProviderIsSilent(t *testing.T) {
	rep := captureUI(t)
	r := &previewReadiness{provider: &readyProvider{}}

	reason, err := r.reason(context.Background(), preview.OpCreate)
	if err != nil || reason != "" {
		t.Fatalf("reason = (%q, %v), want (\"\", nil)", reason, err)
	}
	if got := skipLabels(rep); len(got) != 0 {
		t.Errorf("a ready provider reported %v", got)
	}
}

// TestPreviewReadiness_ReportsEachReasonOnce pins the deduplication.
// The two halves of a provider's lifecycle usually go unwired together
// and answer identically, and the same sentence on the deploy row and
// the teardown row reads as two separate problems.
func TestPreviewReadiness_ReportsEachReasonOnce(t *testing.T) {
	rep := captureUI(t)
	unwired := fmt.Errorf("%w: no pipeline", preview.ErrNotConfigured)
	r := &previewReadiness{provider: &readyProvider{errs: map[preview.Operation]error{
		preview.OpCreate:  unwired,
		preview.OpDestroy: unwired,
	}}}

	for _, op := range []preview.Operation{preview.OpDestroy, preview.OpCreate} {
		reason, err := r.reason(context.Background(), op)
		if err != nil {
			t.Fatalf("reason(%s) errored: %v", op, err)
		}
		// Every caller still gets the reason for its own row, even
		// though only the first announced it.
		if reason != unwired.Error() {
			t.Errorf("reason(%s) = %q, want the provider's message", op, reason)
		}
	}

	if got := skipLabels(rep); len(got) != 1 || got[0] != unwired.Error() {
		t.Errorf("skips = %v, want exactly one carrying the provider's message", got)
	}
}

// TestPreviewReadiness_DistinctReasonsBothReported is the other side of
// the dedup: a provider wired for one half and not the other has two
// things to say, and suppressing the second would hide one of them.
func TestPreviewReadiness_DistinctReasonsBothReported(t *testing.T) {
	rep := captureUI(t)
	r := &previewReadiness{provider: &readyProvider{errs: map[preview.Operation]error{
		preview.OpCreate:  fmt.Errorf("%w: no up workflow", preview.ErrNotConfigured),
		preview.OpDestroy: fmt.Errorf("%w: no down workflow", preview.ErrNotConfigured),
	}}}

	for _, op := range []preview.Operation{preview.OpCreate, preview.OpDestroy} {
		if _, err := r.reason(context.Background(), op); err != nil {
			t.Fatalf("reason(%s) errored: %v", op, err)
		}
	}
	if got := skipLabels(rep); len(got) != 2 {
		t.Errorf("skips = %v, want both reasons", got)
	}
}

// TestPreviewReadiness_FaultIsNotASkip separates the two failure kinds.
// An error that is not "no backend" means answering the question
// failed; reporting it as a skip would drop the step and swallow the
// diagnosis.
func TestPreviewReadiness_FaultIsNotASkip(t *testing.T) {
	rep := captureUI(t)
	boom := errors.New("resolving workflow targets: malformed target")
	r := &previewReadiness{provider: &readyProvider{errs: map[preview.Operation]error{
		preview.OpCreate: boom,
	}}}

	reason, err := r.reason(context.Background(), preview.OpCreate)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the provider's fault", err)
	}
	if reason != "" {
		t.Errorf("reason = %q, want no skip reason alongside a fault", reason)
	}
	if got := skipLabels(rep); len(got) != 0 {
		t.Errorf("a fault was reported as a skip: %v", got)
	}
}

// TestBuildTeardownAction_UnwiredProviderStillRows pins that the row
// survives. ActionSkipped would omit it and take the reason with it —
// the silent exit this command used to produce.
func TestBuildTeardownAction_UnwiredProviderStillRows(t *testing.T) {
	captureUI(t)
	p := &readyProvider{errs: map[preview.Operation]error{
		preview.OpDestroy: fmt.Errorf("%w: no pipeline", preview.ErrNotConfigured),
	}}
	r := &previewReadiness{provider: p}

	action, err := buildTeardownAction(context.Background(), r, p, "brave-falcon", "EX-1")
	if err != nil {
		t.Fatalf("buildTeardownAction: %v", err)
	}
	if action.Apply != nil {
		t.Error("the no-op row carries an Apply; it would tear down through a provider that said it cannot")
	}

	state, detail, err := action.Assess(context.Background())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if state != ActionCompleted {
		t.Errorf("state = %v, want ActionCompleted so the row renders", state)
	}
	if detail != p.errs[preview.OpDestroy].Error() {
		t.Errorf("detail = %q, want the provider's own reason", detail)
	}
}

// TestBuildTeardownAction_ReadyProviderTearsDown is the control: a
// wired provider gets the operative row, with Assess reporting the env
// name rather than a reason.
func TestBuildTeardownAction_ReadyProviderTearsDown(t *testing.T) {
	captureUI(t)
	p := &readyProvider{}
	r := &previewReadiness{provider: p}

	action, err := buildTeardownAction(context.Background(), r, p, "brave-falcon", "EX-1")
	if err != nil {
		t.Fatalf("buildTeardownAction: %v", err)
	}
	if action.Apply == nil {
		t.Fatal("a ready provider's teardown row has no Apply")
	}
	state, detail, err := action.Assess(context.Background())
	if err != nil || state != ActionNeeded || detail != "brave-falcon" {
		t.Errorf("Assess = (%v, %q, %v), want (ActionNeeded, the env name, nil)", state, detail, err)
	}
}

// TestBuildTeardownAction_FaultAborts pins that a fault stops the
// command rather than becoming a quiet no-op row.
func TestBuildTeardownAction_FaultAborts(t *testing.T) {
	captureUI(t)
	boom := errors.New("malformed target")
	p := &readyProvider{errs: map[preview.Operation]error{preview.OpDestroy: boom}}

	_, err := buildTeardownAction(context.Background(), &previewReadiness{provider: p}, p, "brave-falcon", "EX-1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the fault to abort", err)
	}
}

// TestBuildDeployAction_UnwiredProviderResolvesNoInputs is the reason
// readiness is settled before the inputs rather than inside Assess:
// resolving them detects affected repos, pushes branches, and can put
// a selection form on screen. None of that should happen for a deploy
// the provider has already said it cannot carry out — and here it
// cannot, since the workspace path is empty and resolution would fail
// outright if it were reached.
func TestBuildDeployAction_UnwiredProviderResolvesNoInputs(t *testing.T) {
	captureUI(t)
	p := &readyProvider{errs: map[preview.Operation]error{
		preview.OpCreate: fmt.Errorf("%w: no pipeline", preview.ErrNotConfigured),
	}}

	action, prs, err := buildDeployAction(
		&cobra.Command{Use: "preview"}, context.Background(),
		&previewReadiness{provider: p}, "", p, "EX-1",
		previewResolution{previewName: "brave-falcon", deployName: "brave-falcon"},
	)
	if err != nil {
		t.Fatalf("buildDeployAction: %v", err)
	}
	if prs != nil {
		t.Errorf("prs = %v, want none — no inputs should have been resolved", prs)
	}
	if action.Apply != nil {
		t.Error("the no-op row carries an Apply; it would deploy through a provider that said it cannot")
	}
	state, detail, err := action.Assess(context.Background())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if state != ActionCompleted || detail != p.errs[preview.OpCreate].Error() {
		t.Errorf("Assess = (%v, %q), want a rendered row carrying the provider's reason", state, detail)
	}
}
