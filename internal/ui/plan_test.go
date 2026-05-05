package ui

import (
	"strings"
	"testing"
)

func TestPlan_IsEmpty(t *testing.T) {
	p := NewPlan()
	if !p.IsEmpty() {
		t.Error("new plan should be empty")
	}

	p.Add(PlanCreate, "deploy", "repo", "api", "")
	if p.IsEmpty() {
		t.Error("plan with items should not be empty")
	}
}

func TestPlan_HasChanges(t *testing.T) {
	tests := []struct {
		name string
		ops  []PlanOp
		want bool
	}{
		{
			name: "empty plan has no changes",
			ops:  nil,
			want: false,
		},
		{
			name: "only no-change items",
			ops:  []PlanOp{PlanNoChange, PlanNoChange},
			want: false,
		},
		{
			name: "only detail items",
			ops:  []PlanOp{PlanDetail, PlanDetail},
			want: false,
		},
		{
			name: "mixed no-change and detail",
			ops:  []PlanOp{PlanNoChange, PlanDetail},
			want: false,
		},
		{
			name: "create is a change",
			ops:  []PlanOp{PlanNoChange, PlanCreate},
			want: true,
		},
		{
			name: "modify is a change",
			ops:  []PlanOp{PlanModify},
			want: true,
		},
		{
			name: "destroy is a change",
			ops:  []PlanOp{PlanDestroy},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlan()
			for i, op := range tt.ops {
				p.Add(op, "action", "type", "name"+strings.Repeat("x", i), "")
			}
			if got := p.HasChanges(); got != tt.want {
				t.Errorf("HasChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlan_Add(t *testing.T) {
	p := NewPlan()
	p.Add(PlanCreate, "deploy", "repo", "api", "new service")
	p.Add(PlanModify, "update", "env", "staging", "scale to 3")

	if len(p.items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(p.items))
	}

	item := p.items[0]
	if item.Op != PlanCreate || item.Action != "deploy" || item.Type != "repo" || item.Name != "api" || item.Detail != "new service" {
		t.Errorf("first item mismatch: %+v", item)
	}

	item = p.items[1]
	if item.Op != PlanModify || item.Action != "update" {
		t.Errorf("second item mismatch: %+v", item)
	}
}

func TestPlan_Add_Chaining(t *testing.T) {
	p := NewPlan().
		Add(PlanCreate, "a", "b", "c", "").
		Add(PlanModify, "d", "e", "f", "")

	if len(p.items) != 2 {
		t.Fatalf("chained Add: expected 2 items, got %d", len(p.items))
	}
}

func TestPlan_Summary(t *testing.T) {
	tests := []struct {
		name     string
		ops      []PlanOp
		contains []string
		excludes []string
	}{
		{
			name:     "create only",
			ops:      []PlanOp{PlanCreate, PlanCreate},
			contains: []string{"2 to create"},
			excludes: []string{"update", "destroy", "unchanged"},
		},
		{
			name:     "modify only",
			ops:      []PlanOp{PlanModify},
			contains: []string{"1 to update"},
		},
		{
			name:     "destroy only",
			ops:      []PlanOp{PlanDestroy, PlanDestroy, PlanDestroy},
			contains: []string{"3 to destroy"},
		},
		{
			name:     "no-change only",
			ops:      []PlanOp{PlanNoChange},
			contains: []string{"1 unchanged"},
		},
		{
			name:     "detail counted with create",
			ops:      []PlanOp{PlanCreate, PlanDetail},
			contains: []string{"2 to create"},
		},
		{
			name:     "mixed plan",
			ops:      []PlanOp{PlanCreate, PlanModify, PlanNoChange},
			contains: []string{"1 to create", "1 to update", "1 unchanged"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlan()
			for i, op := range tt.ops {
				p.Add(op, "action", "type", "name"+strings.Repeat("x", i), "")
			}
			got := p.Summary()
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("Summary() = %q, missing %q", got, want)
				}
			}
			for _, nope := range tt.excludes {
				if strings.Contains(got, nope) {
					t.Errorf("Summary() = %q, should not contain %q", got, nope)
				}
			}
		})
	}
}

func TestPlan_SummaryPastTense(t *testing.T) {
	p := NewPlan()
	p.Add(PlanCreate, "deploy", "repo", "api", "")
	p.Add(PlanModify, "update", "env", "staging", "")
	p.Add(PlanDestroy, "remove", "channel", "old", "")
	p.Add(PlanNoChange, "check", "repo", "lib", "")
	p.Add(PlanDetail, "add", "webhook", "notify", "")

	got := p.SummaryPastTense()
	for _, want := range []string{"2 created", "1 updated", "1 destroyed", "1 unchanged"} {
		if !strings.Contains(got, want) {
			t.Errorf("SummaryPastTense() = %q, missing %q", got, want)
		}
	}
}

func TestPlan_SummaryPartial(t *testing.T) {
	tests := []struct {
		name      string
		succeeded int
		failed    int
		details   int // number of PlanDetail items to add
		contains  []string
	}{
		{
			name:      "some failed some succeeded",
			succeeded: 2,
			failed:    1,
			contains:  []string{"1 failed", "2 applied"},
		},
		{
			name:      "all failed",
			succeeded: 0,
			failed:    3,
			contains:  []string{"3 failed"},
		},
		{
			name:      "all succeeded",
			succeeded: 2,
			failed:    0,
			contains:  []string{"2 applied"},
		},
		{
			name:      "detail items add to succeeded",
			succeeded: 1,
			failed:    0,
			details:   2,
			contains:  []string{"3 applied"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPlan()
			// Add enough items to make a valid plan.
			for i := 0; i < tt.succeeded+tt.failed; i++ {
				p.Add(PlanCreate, "a", "b", "c"+strings.Repeat("x", i), "")
			}
			for i := 0; i < tt.details; i++ {
				p.Add(PlanDetail, "a", "b", "d"+strings.Repeat("x", i), "")
			}

			got := p.SummaryPartial(tt.succeeded, tt.failed)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("SummaryPartial(%d, %d) = %q, missing %q",
						tt.succeeded, tt.failed, got, want)
				}
			}
		})
	}
}

func TestPlan_ColumnWidths(t *testing.T) {
	p := NewPlan()
	p.Add(PlanCreate, "deploy", "repo", "api", "")
	p.Add(PlanModify, "up", "environment", "staging-us-west-2", "")

	w := p.columnWidths()

	if w.action != len("deploy") {
		t.Errorf("action width = %d, want %d", w.action, len("deploy"))
	}
	if w.typ != len("environment") {
		t.Errorf("type width = %d, want %d", w.typ, len("environment"))
	}
	if w.name != len("staging-us-west-2") {
		t.Errorf("name width = %d, want %d", w.name, len("staging-us-west-2"))
	}
}

func TestPlan_ColumnWidths_Empty(t *testing.T) {
	p := NewPlan()
	w := p.columnWidths()
	if w.action != 0 || w.typ != 0 || w.name != 0 {
		t.Errorf("empty plan columnWidths: got %+v, want all zeros", w)
	}
}

func TestPlanSymbol(t *testing.T) {
	tests := []struct {
		name       string
		op         PlanOp
		wantSymbol string
	}{
		{"create", PlanCreate, "+"},
		{"modify", PlanModify, "~"},
		{"destroy", PlanDestroy, "-"},
		{"no-change", PlanNoChange, "="},
		{"detail", PlanDetail, "+"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			symbol, style := planSymbol(tt.op)
			if symbol != tt.wantSymbol {
				t.Errorf("planSymbol(%d) symbol = %q, want %q", tt.op, symbol, tt.wantSymbol)
			}
			// Style should be non-zero (has a foreground color set).
			rendered := style.Render(symbol)
			if rendered == "" {
				t.Errorf("planSymbol(%d) style rendered empty string", tt.op)
			}
		})
	}

	// Unknown op falls through to default.
	t.Run("unknown op", func(t *testing.T) {
		symbol, _ := planSymbol(PlanOp(99))
		if symbol != " " {
			t.Errorf("planSymbol(99) = %q, want %q", symbol, " ")
		}
	})
}
