package ui

import (
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
func Complete(label string) { defaultReporter.Complete(label) }

// CompleteWithDetail prints a completed step with indented detail items.
func CompleteWithDetail(label string, items []string) { defaultReporter.CompleteDetail(label, items) }

// Skip prints a skipped step with a warning symbol.
func Skip(label string) { defaultReporter.Skip(label) }

// Fail prints a failed step with an error symbol.
func Fail(label string) { defaultReporter.Fail(label) }
