package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

var (
	successStyle = lipgloss.NewStyle().Foreground(Theme.Success)
	errorStyle   = lipgloss.NewStyle().Foreground(Theme.Error)
	warningStyle = lipgloss.NewStyle().Foreground(Theme.Warning)
	mutedStyle   = lipgloss.NewStyle().Foreground(Theme.Muted)
	primaryStyle = lipgloss.NewStyle().Foreground(Theme.Primary)
	boldStyle    = lipgloss.NewStyle().Bold(true)
)

// Success prints a success message with a check mark.
func Success(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(successStyle.Render(Theme.Check) + " " + text)
}

// Error prints an error message with an X mark to stderr.
func Error(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, errorStyle.Render(Theme.Cross) + " " + text)
}

// Warning prints a warning message to stderr.
func Warning(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, warningStyle.Render("!") + " " + text)
}

// Info prints an informational message.
func Info(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(primaryStyle.Render(Theme.Bullet) + " " + text)
}

// Muted prints a dimmed/secondary message.
func Muted(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(mutedStyle.Render(text))
}

// Bold prints bold text.
func Bold(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(boldStyle.Render(text))
}

// Item prints an indented item with an arrow, typically under a heading.
func Item(label, value string) {
	fmt.Printf("  %s %s\n",
		mutedStyle.Render(label),
		primaryStyle.Render(value),
	)
}

// DryRun prints a dry-run prefixed message.
func DryRun(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	prefix := warningStyle.Render("[dry-run]")
	fmt.Printf("%s %s\n", prefix, text)
}
