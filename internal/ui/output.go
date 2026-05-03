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

// Error prints an error message to stderr. In raw mode, outputs a
// plain "error: ..." line without color or glyphs. In interactive
// mode, uses the styled ✗ prefix.
func Error(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	if IsRaw() {
		fmt.Fprintln(os.Stderr, "error: "+text)
	} else {
		fmt.Fprintln(os.Stderr, errorStyle.Render(Palette.Cross)+" "+text)
	}
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

// Selected prints feedback that a single value was chosen interactively.
// The label is the field title and value is the user's selection.
func Selected(label, value string) { defaultReporter.Selected(label, value) }

// SelectedMulti prints feedback that multiple values were chosen interactively.
// The label is the field title and values are the user's selections.
func SelectedMulti(label string, values []string) { defaultReporter.SelectedMulti(label, values) }

// Header prints a styled command header. Pass the command name and optional
// context (e.g., issue key, workspace name).
func Header(command string, context ...string) { defaultReporter.Header(command, context...) }

// DryRun prints a dry-run prefixed message.
func DryRun(msg string, args ...any) { defaultReporter.DryRun(msg, args...) }

// Details prints a block of key-value pairs with an optional heading.
func Details(heading string, fields Fields) { defaultReporter.Details(heading, fields) }
