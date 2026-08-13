package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestPlanCard_TitleWord(t *testing.T) {
	tests := []struct {
		state PlanCardState
		want  string
	}{
		{PlanProposed, "Pending"},
		{PlanVerified, "Verified"},
		{PlanApplying, "Applying"},
		{PlanSuccess, "Success"},
		{PlanPartial, "Partial"},
		{PlanFailure, "Failure"},
		{PlanCancelled, "Cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			plan := NewPlan().Add(PlanCreate, "deploy", "repo", "api", "")
			pc := NewPlanCard(plan)
			pc.SetState(tt.state)
			if got := pc.titleWord(); got != tt.want {
				t.Errorf("titleWord() = %q, want %q", got, tt.want)
			}
		})
	}

	// Unknown state falls through to default "Pending".
	t.Run("unknown state defaults to Pending", func(t *testing.T) {
		plan := NewPlan().Add(PlanCreate, "deploy", "repo", "api", "")
		pc := NewPlanCard(plan)
		pc.SetState(PlanCardState(99))
		if got := pc.titleWord(); got != "Pending" {
			t.Errorf("titleWord() for unknown state = %q, want %q", got, "Pending")
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
	pc.SetResults(1, 1, 0)
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
		skipped   int
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
		{
			// The gate closed on an assess failure, so no action
			// errored — but the plan did not fully apply, and a ✓
			// Success card would say it had.
			name:      "skipped action without an apply error yields partial",
			err:       nil,
			succeeded: 1,
			skipped:   1,
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
				skipped:   tt.skipped,
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
		{PlanVerified, cardGlyphSuccess},
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

// TestPlanCard_ApplyActions_Gating covers the apply queue's execution
// contract: ungated actions are best-effort, gated ones are not.
func TestPlanCard_ApplyActions_Gating(t *testing.T) {
	// record builds an action that appends its name to ran.
	record := func(ran *[]string, name string, err error, gated bool) (PlanAction, *SkipRef) {
		skip := &SkipRef{}
		return PlanAction{
			Run: func() error {
				*ran = append(*ran, name)
				return err
			},
			RequiresPriorSuccess: gated,
			Skip:                 skip,
		}, skip
	}

	t.Run("gated action is skipped behind a failed action", func(t *testing.T) {
		var ran []string
		deploy, _ := record(&ran, "deploy", errSentinel, false)
		status, statusSkip := record(&ran, "status", nil, true)

		pc := NewPlanCard(NewPlan().
			Add(PlanCreate, "deploy", "service", "api", "").
			Add(PlanModify, "status", "issue", "EX-1", ""))
		got := pc.applyActions([]PlanAction{deploy, status})

		if slices.Contains(ran, "status") {
			t.Errorf("gated action ran behind a failure; ran = %v", ran)
		}
		if got.skipped != 1 || got.failed != 1 || got.succeeded != 0 {
			t.Errorf("result = %+v, want 1 failed, 1 skipped, 0 succeeded", got)
		}
		if !errors.Is(got.err, errSentinel) {
			t.Errorf("err = %v, want the failing action's error", got.err)
		}
		if !statusSkip.Get() {
			t.Error("skip ref not marked; the plan row would still read as applied")
		}
	})

	t.Run("ungated actions stay best-effort", func(t *testing.T) {
		var ran []string
		first, _ := record(&ran, "first", errSentinel, false)
		second, _ := record(&ran, "second", nil, false)

		pc := NewPlanCard(NewPlan().Add(PlanDestroy, "worktree", "repo", "api", ""))
		got := pc.applyActions([]PlanAction{first, second})

		if !slices.Contains(ran, "second") {
			t.Errorf("independent action was withheld behind a failure; ran = %v", ran)
		}
		if got.skipped != 0 || got.succeeded != 1 || got.failed != 1 {
			t.Errorf("result = %+v, want 1 failed, 1 applied, 0 skipped", got)
		}
	})

	t.Run("gated action is skipped behind an assess failure", func(t *testing.T) {
		// No action errors — the ✗ row is the only failure, and it
		// happened before the queue was built.
		var ran []string
		status, statusSkip := record(&ran, "status", nil, true)

		pc := NewPlanCard(NewPlan().
			Add(PlanFailed, "deploy", "service", "api", "deployments API 500").
			Add(PlanModify, "status", "issue", "EX-1", ""))
		got := pc.applyActions([]PlanAction{status})

		if len(ran) != 0 {
			t.Errorf("gated action ran past an assess failure; ran = %v", ran)
		}
		if got.skipped != 1 || got.err != nil {
			t.Errorf("result = %+v, want 1 skipped and no apply error", got)
		}
		if !statusSkip.Get() {
			t.Error("skip ref not marked")
		}
	})

	t.Run("gated action runs on a clean plan", func(t *testing.T) {
		var ran []string
		deploy, _ := record(&ran, "deploy", nil, false)
		status, statusSkip := record(&ran, "status", nil, true)

		pc := NewPlanCard(NewPlan().
			Add(PlanCreate, "deploy", "service", "api", "").
			Add(PlanModify, "status", "issue", "EX-1", ""))
		got := pc.applyActions([]PlanAction{deploy, status})

		if !slices.Contains(ran, "status") {
			t.Errorf("gated action withheld from a clean run; ran = %v", ran)
		}
		if got.succeeded != 2 || got.skipped != 0 || got.err != nil {
			t.Errorf("result = %+v, want 2 applied and nothing skipped", got)
		}
		if statusSkip.Get() {
			t.Error("skip ref marked on a clean run")
		}
	})
}

// TestPlan_SkippedRowRendering checks that a marked row stops
// advertising the change it never made.
func TestPlan_SkippedRowRendering(t *testing.T) {
	skip := &SkipRef{}
	p := NewPlan().AddWithRefs(PlanModify, "status", "issue", "EX-1",
		"In Progress → Done", nil, skip)

	before := p.RenderItems()
	if strings.Contains(before, "skipped") {
		t.Errorf("unmarked row already reads as skipped: %q", before)
	}

	skip.Set()
	after := p.RenderItems()
	if !strings.Contains(after, "(skipped)") {
		t.Errorf("marked row = %q, want it to say it was skipped", after)
	}
	if !strings.Contains(after, cardGlyphSkipped) {
		t.Errorf("marked row = %q, want the skipped glyph %q", after, cardGlyphSkipped)
	}
	if strings.Contains(after, "~") {
		t.Errorf("marked row = %q, still shows the modify symbol it never applied", after)
	}
}

// errSentinel is a reusable non-nil error for tests.
var errSentinel = fmt.Errorf("test error")
