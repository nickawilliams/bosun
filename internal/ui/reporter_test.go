package ui

import (
	"fmt"
	"testing"
)

func TestNewFields(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  Fields
	}{
		{
			name:  "empty input",
			input: nil,
			want:  Fields{},
		},
		{
			name:  "single pair",
			input: []string{"key", "value"},
			want:  Fields{{Key: "key", Value: "value"}},
		},
		{
			name:  "multiple pairs",
			input: []string{"a", "1", "b", "2", "c", "3"},
			want: Fields{
				{Key: "a", Value: "1"},
				{Key: "b", Value: "2"},
				{Key: "c", Value: "3"},
			},
		},
		{
			name:  "odd element dropped",
			input: []string{"key", "value", "orphan"},
			want:  Fields{{Key: "key", Value: "value"}},
		},
		{
			name:  "single element dropped",
			input: []string{"lonely"},
			want:  Fields{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewFields(tt.input...)
			if len(got) != len(tt.want) {
				t.Fatalf("NewFields() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("NewFields()[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsRaw_DefaultIsFalse(t *testing.T) {
	// Save and restore the default reporter.
	prev := defaultReporter
	t.Cleanup(func() { defaultReporter = prev })

	// The default reporter (cardReporter) should not be raw.
	defaultReporter = newCardReporter()
	if IsRaw() {
		t.Error("IsRaw() should return false for the default cardReporter")
	}
}

func TestIsRaw_TrueAfterSetRaw(t *testing.T) {
	prev := defaultReporter
	t.Cleanup(func() { defaultReporter = prev })

	SetDefault(NewRawReporter())
	if !IsRaw() {
		t.Error("IsRaw() should return true after SetDefault(NewRawReporter())")
	}
}

func TestRawReporter_TaskRunsFunction(t *testing.T) {
	r := NewRawReporter()

	called := false
	err := r.Task("test", func() error {
		called = true
		return nil
	})
	if !called {
		t.Error("Task should run the provided function")
	}
	if err != nil {
		t.Errorf("Task returned unexpected error: %v", err)
	}
}

func TestRawReporter_TaskReturnsError(t *testing.T) {
	r := NewRawReporter()

	want := fmt.Errorf("task failed")
	err := r.Task("test", func() error {
		return want
	})
	if err != want {
		t.Errorf("Task error = %v, want %v", err, want)
	}
}

func TestRawReporter_GroupRunsCallback(t *testing.T) {
	r := NewRawReporter()

	called := false
	var innerReporter Reporter
	r.Group("test", func(g Reporter) {
		called = true
		innerReporter = g
	})
	if !called {
		t.Error("Group should run the provided callback")
	}
	// The inner reporter should also be a rawReporter.
	if _, ok := innerReporter.(*rawReporter); !ok {
		t.Errorf("inner reporter should be *rawReporter, got %T", innerReporter)
	}
}
