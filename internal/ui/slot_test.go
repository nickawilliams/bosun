package ui

import "testing"

func TestSlot_ClearEmpty(t *testing.T) {
	s := NewSlot()
	// Clearing an empty slot must not panic.
	s.Clear()
}

func TestSlot_FinalizeIdempotent(t *testing.T) {
	s := NewSlot()
	s.Finalize()
	// Second finalize must not panic.
	s.Finalize()
}

func TestSlot_PanicsAfterFinalize(t *testing.T) {
	tests := []struct {
		name string
		call func(s *Slot)
	}{
		{"Show", func(s *Slot) { s.Show(NewCard(CardInfo, "x")) }},
		{"Clear", func(s *Slot) { s.Clear() }},
		{"Run", func(s *Slot) { _ = s.Run("x", func() error { return nil }) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSlot()
			s.Finalize()

			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s on finalized slot did not panic", tt.name)
				}
				msg, ok := r.(string)
				if !ok || msg != "slot: use after finalize" {
					t.Fatalf("unexpected panic: %v", r)
				}
			}()

			tt.call(s)
		})
	}
}
