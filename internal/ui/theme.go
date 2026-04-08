package ui

import "github.com/charmbracelet/lipgloss"

// Theme defines the color palette and base styles used throughout bosun.
var Theme = struct {
	// Colors
	Primary   lipgloss.Color
	Success   lipgloss.Color
	Warning   lipgloss.Color
	Error     lipgloss.Color
	Muted     lipgloss.Color
	Highlight lipgloss.Color

	// Symbols
	Check  string
	Cross  string
	Arrow  string
	Bullet string
	Dot    string
}{
	Primary:   lipgloss.Color("12"),  // Blue
	Success:   lipgloss.Color("10"),  // Green
	Warning:   lipgloss.Color("11"),  // Yellow
	Error:     lipgloss.Color("9"),   // Red
	Muted:     lipgloss.Color("240"), // Gray
	Highlight: lipgloss.Color("14"),  // Cyan

	Check:  "✓",
	Cross:  "✗",
	Arrow:  "→",
	Bullet: "•",
	Dot:    "·",
}
