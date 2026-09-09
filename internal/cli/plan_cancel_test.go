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

// TestPlanConfirmSummary pins the compact confirm's caption: action
// count always, workspace-group count only when the queue spans more
// than one group (a bulk plan).
func TestPlanConfirmSummary(t *testing.T) {
	tests := []struct {
		name    string
		actions []PlanAction
		want    string
	}{
		{"single_action", make([]PlanAction, 1), "1 action"},
		{"ungrouped_actions", make([]PlanAction, 3), "3 actions"},
		{
			"one_group_stays_countless",
			[]PlanAction{{Group: "ws-a"}, {Group: "ws-a"}},
			"2 actions",
		},
		{
			"multiple_groups_count_workspaces",
			[]PlanAction{{Group: "ws-a"}, {Group: "ws-a"}, {Group: "ws-b"}, {}},
			"4 actions across 2 workspaces",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planConfirmSummary(tt.actions); got != tt.want {
				t.Errorf("planConfirmSummary = %q, want %q", got, tt.want)
			}
		})
	}
}
