package ui

import (
	"os"

	"golang.org/x/term"
)

// TermWidth returns the current terminal width, defaulting to 80.
func TermWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// IsTerminal reports whether stdout is a TTY.
func IsTerminal() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
