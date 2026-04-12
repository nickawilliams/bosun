package ui

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
)

var (
	successStyle = lipgloss.NewStyle().Foreground(Palette.Success)
	errorStyle   = lipgloss.NewStyle().Foreground(Palette.Error)
	warningStyle = lipgloss.NewStyle().Foreground(Palette.Warning)
	mutedStyle   = lipgloss.NewStyle().Foreground(Palette.Muted)
	primaryStyle = lipgloss.NewStyle().Foreground(Palette.Primary)
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

// Success prints a success message with a check mark.
func Success(msg string, args ...any) { defaultReporter.Success(msg, args...) }

// Error prints an error message with an X mark to stderr.
func Error(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, errorStyle.Render(Palette.Cross)+" "+text)
}

// Warning prints a warning message to stderr.
func Warning(msg string, args ...any) { defaultReporter.Warning(msg, args...) }

// Info prints an informational message.
func Info(msg string, args ...any) { defaultReporter.Info(msg, args...) }

// Muted prints a dimmed/secondary message.
func Muted(msg string, args ...any) { defaultReporter.Muted(msg, args...) }

// Bold prints bold text.
func Bold(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(boldStyle.Render(text))
}

// Item prints an indented item with a label and value.
func Item(label, value string) {
	fmt.Printf("  %s %s\n",
		mutedStyle.Render(label),
		primaryStyle.Render(value),
	)
}

// Saved prints a confirmation that a value was set, styled to match huh form
// inputs: check + bold primary label on one line, muted value on the next.
func Saved(label, value string) { defaultReporter.Saved(label, value) }

// Header prints a styled command header. Pass the command name and optional
// context (e.g., issue key, workspace name).
func Header(command string, context ...string) { defaultReporter.Header(command, context...) }

// DryRun prints a dry-run prefixed message.
func DryRun(msg string, args ...any) { defaultReporter.DryRun(msg, args...) }
