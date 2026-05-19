package ui

import "time"

type taskDoneMsg struct{ err error }

// minSpinnerDuration is the floor on how long a spinner-driven
// BubbleTea program runs before it tears down. BubbleTea v2 emits
// terminal-capability queries during setup (DA1, kitty keyboard
// `CSI ? u`, etc.); if the program quits before those query
// responses are consumed, the responses leak into stdin and show
// up in the user's shell input (e.g., `1u` from a kitty keyboard
// response). 250ms is enough cycles for slower queries like the
// kitty keyboard one to round-trip on first invocation, while
// staying short enough that genuinely fast operations don't feel
// laggy.
const minSpinnerDuration = 250 * time.Millisecond

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
