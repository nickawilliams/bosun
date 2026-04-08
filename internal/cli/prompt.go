package cli

import (
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"
)

// isInteractive returns true if stdin is a terminal.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// promptRequired prompts for a value if stdin is a terminal. If stdin is not
// a terminal (piped/scripted), returns empty string and the caller should
// error.
func promptRequired(label string) string {
	if !isInteractive() {
		return ""
	}

	var value string
	err := huh.NewInput().
		Title(label).
		Value(&value).
		Run()
	if err != nil {
		return ""
	}
	return value
}

// promptConfirm shows a yes/no confirmation. Returns true if confirmed.
// Defaults to the provided value. Returns defaultVal in non-interactive mode.
func promptConfirm(label string, defaultVal bool) bool {
	if !isInteractive() {
		return defaultVal
	}

	var confirmed bool
	err := huh.NewConfirm().
		Title(label).
		Affirmative("Yes").
		Negative("No").
		Value(&confirmed).
		Run()
	if err != nil {
		return defaultVal
	}
	return confirmed
}

// promptSelect shows a selection prompt. Returns the selected value.
// Returns empty string in non-interactive mode.
func promptSelect(label string, options []string) string {
	if !isInteractive() {
		return ""
	}

	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o, o)
	}

	var value string
	err := huh.NewSelect[string]().
		Title(label).
		Options(opts...).
		Value(&value).
		Run()
	if err != nil {
		return ""
	}
	return value
}

// promptValue displays a prompt with a default value. Used by init.
func promptValue(label, defaultVal string) string {
	if !isInteractive() {
		return defaultVal
	}

	value := defaultVal
	err := huh.NewInput().
		Title(label).
		Value(&value).
		Run()
	if err != nil {
		return defaultVal
	}
	return value
}
