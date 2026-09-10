package cli

import (
	"errors"
	"testing"
)

// TestErrPlanCancelledIsErrCancelled locks the sentinel contract:
// errPlanCancelled must satisfy every existing errors.Is(err,
// ErrCancelled) check (commands treat it as a normal cancel/abort)
// while remaining distinguishable so HandleError can suppress the
// redundant trailing card when the Cancelled plan card is on screen.
func TestErrPlanCancelledIsErrCancelled(t *testing.T) {
	if !errors.Is(errPlanCancelled, ErrCancelled) {
		t.Error("errPlanCancelled must wrap ErrCancelled")
	}
	if errors.Is(ErrCancelled, errPlanCancelled) {
		t.Error("plain ErrCancelled must NOT match errPlanCancelled — only plan-rendered cancels are suppressed")
	}
}
