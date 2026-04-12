package cli

import (
	"os"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"golang.org/x/term"
)

// formTheme is the shared huh theme derived from the app palette.
var formTheme = ui.FormTheme()

// isInteractive returns true if stdin is a terminal.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// runForm runs a huh form with the app theme applied. Keybinding
// help is explicitly enabled so the shortcut hints render in the
// footer beneath the active prompt. The timeline layout wraps
// huh's default layout to recolor field separator bars in the
// recessed timeline gray.
func runForm(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(formTheme).
		WithLayout(ui.NewTimelineLayout()).
		WithShowHelp(true).
		Run()
}

// promptRequired prompts for a value if stdin is a terminal. If stdin is not
// a terminal (piped/scripted), returns empty string and the caller should
// error.
func promptRequired(label string) string {
	if !isInteractive() {
		return ""
	}

	var value string
	if err := runForm(huh.NewInput().Title(label).Value(&value)); err != nil {
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

	confirmed := defaultVal
	err := runForm(
		huh.NewConfirm().
			Title(label).
			Affirmative("Yes").
			Negative("No").
			Value(&confirmed),
	)
	if err != nil {
		return defaultVal
	}
	return confirmed
}

// promptSecret prompts for a sensitive value with masked input.
// Returns empty string in non-interactive mode.
func promptSecret(label string) string {
	if !isInteractive() {
		return ""
	}

	var value string
	if err := runForm(huh.NewInput().Title(label).EchoMode(huh.EchoModePassword).Value(&value)); err != nil {
		return ""
	}
	return value
}

// promptValue displays a prompt with a default value.
func promptValue(label, defaultVal string) string {
	if !isInteractive() {
		return defaultVal
	}

	value := defaultVal
	if err := runForm(huh.NewInput().Title(label).Value(&value)); err != nil {
		return defaultVal
	}
	return value
}
