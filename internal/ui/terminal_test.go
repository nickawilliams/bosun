package ui

import (
	"bytes"
	"os"
	"testing"
)

// TestTermSizeDefaults pins the fallbacks. Both are load-bearing:
// TermWidth's feeds huh's WithWidth (a zero would panic inside
// textinput), and TermHeight's bounds selection-form heights, where
// guessing too *tall* is the failure mode — so a non-TTY must answer
// with the classic 24 rows rather than something optimistic.
func TestTermSizeDefaults(t *testing.T) {
	t.Cleanup(ResetStreams)

	SetStreams(nil, &bytes.Buffer{}, nil)
	if got := TermWidth(); got != 80 {
		t.Errorf("TermWidth() = %d for a non-file output, want the 80-column default", got)
	}
	if got := TermHeight(); got != 24 {
		t.Errorf("TermHeight() = %d for a non-file output, want the 24-row default", got)
	}

	// A real *os.File that isn't a terminal takes the syscall-failed
	// branch rather than the type-assertion one — same answers.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	SetStreams(nil, w, nil)
	if got := TermHeight(); got != 24 {
		t.Errorf("TermHeight() = %d for a pipe, want the 24-row default", got)
	}
}
