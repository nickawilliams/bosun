package ui

import (
	"fmt"
	"os"
	"strings"

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
func Success(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(successStyle.Render(Palette.Check) + " " + text)
}

// Error prints an error message with an X mark to stderr.
func Error(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, errorStyle.Render(Palette.Cross)+" "+text)
}

// Warning prints a warning message to stderr.
func Warning(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Fprintln(os.Stderr, warningStyle.Render("!")+" "+text)
}

// Info prints an informational message.
func Info(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	fmt.Println(primaryStyle.Render(Palette.Bullet) + " " + text)
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

// Item prints an indented item with a label and value.
func Item(label, value string) {
	fmt.Printf("  %s %s\n",
		mutedStyle.Render(label),
		primaryStyle.Render(value),
	)
}

// Saved prints a confirmation that a value was set, styled to match huh form
// inputs: check + bold primary label + muted value.
func Saved(label, value string) {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	valueStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	fmt.Printf("  %s %s %s\n",
		stepCheckStyle.Render(Palette.Check),
		titleStyle.Render(label),
		valueStyle.Render(value),
	)
}

// Header prints a styled command header. Pass the command name and optional
// context (e.g., issue key, workspace name).
func Header(command string, context ...string) {
	parts := []string{boldStyle.Render(command)}
	for _, c := range context {
		parts = append(parts, primaryStyle.Render(c))
	}
	symbol := lipgloss.NewStyle().Foreground(Palette.Accent).Render("●")
	fmt.Printf("\n%s %s\n\n", symbol, strings.Join(parts, " "))
}

// DryRun prints a dry-run prefixed message.
func DryRun(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	prefix := warningStyle.Render("[dry-run]")
	fmt.Printf("%s %s\n", prefix, text)
}
