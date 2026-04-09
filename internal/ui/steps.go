package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

var (
	stepCheckStyle = lipgloss.NewStyle().Foreground(Palette.Success)
	stepSkipStyle  = lipgloss.NewStyle().Foreground(Palette.Warning)
	stepFailStyle  = lipgloss.NewStyle().Foreground(Palette.Error)
	stepArrowStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
	stepItemStyle  = lipgloss.NewStyle().Foreground(Palette.NormalFg)
)

// Complete prints a completed step with a green checkmark.
func Complete(label string) {
	fmt.Printf("  %s %s\n", stepCheckStyle.Render(Palette.Check), label)
}

// CompleteWithDetail prints a completed step with indented detail items.
func CompleteWithDetail(label string, items []string) {
	Complete(label)
	for _, item := range items {
		fmt.Printf("      %s %s\n", stepArrowStyle.Render(Palette.Arrow), stepItemStyle.Render(item))
	}
}

// Skip prints a skipped step with a warning symbol.
func Skip(label string) {
	fmt.Printf("  %s %s\n", stepSkipStyle.Render("!"), label)
}

// Fail prints a failed step with an error symbol.
func Fail(label string) {
	fmt.Printf("  %s %s\n", stepFailStyle.Render(Palette.Cross), label)
}
