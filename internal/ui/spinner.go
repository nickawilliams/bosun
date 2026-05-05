package ui

import "time"

type taskDoneMsg struct{ err error }

// minSpinnerDuration is the floor on how long a spinner-driven
// BubbleTea program runs before it tears down. BubbleTea v2 emits
// terminal-mode-query escape sequences during its setup; if the
// program quits before those queries are answered and consumed,
// the escapes leak into the terminal output. 100ms is enough cycles
// for the queries to round-trip without being noticeable on
// genuinely fast operations.
const minSpinnerDuration = 100 * time.Millisecond

// holdSpinner blocks until at least minSpinnerDuration has elapsed
// since start. Call from the end of spinner-driving goroutines —
// before sending the result onto the channel that triggers tea.Quit —
// so the BubbleTea program survives long enough to consume its own
// setup escapes.
func holdSpinner(start time.Time) {
	if d := time.Since(start); d < minSpinnerDuration {
		time.Sleep(minSpinnerDuration - d)
	}
}
