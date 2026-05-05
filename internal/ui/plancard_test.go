package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestPlanCard_Prefix(t *testing.T) {
	tests := []struct {
		state PlanCardState
		want  string
	}{
		{PlanProposed, "Pending:"},
		{PlanApplying, "Applying:"},
		{PlanSuccess, "Success:"},
		{PlanPartial, "Partial:"},
		{PlanFailure, "Failure:"},
		{PlanCancelled, "Cancelled:"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			plan := NewPlan().Add(PlanCreate, "deploy", "repo", "api", "")
			pc := NewPlanCard(plan)
			pc.SetState(tt.state)
			if got := pc.prefix(); got != tt.want {
				t.Errorf("prefix() = %q, want %q", got, tt.want)
			}
		})
	}

	// Unknown state falls through to default "Pending:".
	t.Run("unknown state defaults to Pending", func(t *testing.T) {
		plan := NewPlan().Add(PlanCreate, "deploy", "repo", "api", "")
		pc := NewPlanCard(plan)
		pc.SetState(PlanCardState(99))
		if got := pc.prefix(); got != "Pending:" {
			t.Errorf("prefix() for unknown state = %q, want %q", got, "Pending:")
		}
	})
}

func TestPlanCard_Summary(t *testing.T) {
	plan := NewPlan().
		Add(PlanCreate, "deploy", "repo", "api", "").
		Add(PlanModify, "update", "env", "staging", "")
	pc := NewPlanCard(plan)

	// Proposed state uses present-tense Summary.
	pc.SetState(PlanProposed)
	got := pc.summary()
	if !strings.Contains(got, "to create") {
		t.Errorf("summary() in Proposed state should use present tense, got %q", got)
	}

	// Success state uses past-tense SummaryPastTense.
	pc.SetState(PlanSuccess)
	got = pc.summary()
	if !strings.Contains(got, "created") {
		t.Errorf("summary() in Success state should use past tense, got %q", got)
	}

	// Partial state uses SummaryPartial.
	pc.SetResults(1, 1)
	pc.SetState(PlanPartial)
	got = pc.summary()
	if !strings.Contains(got, "failed") || !strings.Contains(got, "applied") {
		t.Errorf("summary() in Partial state should use partial tense, got %q", got)
	}
}

func TestPlanCard_SetFinalState(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		succeeded int
		want      PlanCardState
	}{
		{
			name:      "no error yields success",
			err:       nil,
			succeeded: 3,
			want:      PlanSuccess,
		},
		{
			name:      "error with no successes yields failure",
			err:       errSentinel,
			succeeded: 0,
			want:      PlanFailure,
		},
		{
			name:      "error with some successes yields partial",
			err:       errSentinel,
			succeeded: 2,
			want:      PlanPartial,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := NewPlan().Add(PlanCreate, "a", "b", "c", "")
			pc := NewPlanCard(plan)

			result := planApplyResult{
				err:       tt.err,
				succeeded: tt.succeeded,
				failed:    0,
			}
			if tt.err != nil {
				result.failed = 1
			}
			pc.setFinalState(result)

			if pc.state != tt.want {
				t.Errorf("setFinalState() state = %d, want %d", pc.state, tt.want)
			}
		})
	}
}

func TestPlanCard_Glyph(t *testing.T) {
	tests := []struct {
		state    PlanCardState
		wantRune string // the raw glyph character embedded in styled output
	}{
		{PlanProposed, cardGlyphInput},
		{PlanApplying, cardGlyphPending},
		{PlanSuccess, cardGlyphSuccess},
		{PlanPartial, cardGlyphSkipped},
		{PlanFailure, cardGlyphFailed},
		{PlanCancelled, cardGlyphSkipped},
	}

	for _, tt := range tests {
		t.Run(tt.wantRune, func(t *testing.T) {
			plan := NewPlan().Add(PlanCreate, "a", "b", "c", "")
			pc := NewPlanCard(plan)
			pc.SetState(tt.state)
			got := pc.glyph()
			if !strings.Contains(got, tt.wantRune) {
				t.Errorf("glyph() for state %d = %q, should contain %q", tt.state, got, tt.wantRune)
			}
		})
	}

	// Unknown state returns space.
	t.Run("unknown state", func(t *testing.T) {
		plan := NewPlan().Add(PlanCreate, "a", "b", "c", "")
		pc := NewPlanCard(plan)
		pc.SetState(PlanCardState(99))
		got := pc.glyph()
		if got != " " {
			t.Errorf("glyph() for unknown state = %q, want %q", got, " ")
		}
	})
}

// errSentinel is a reusable non-nil error for tests.
var errSentinel = fmt.Errorf("test error")
