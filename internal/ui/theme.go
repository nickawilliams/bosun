package ui

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// Palette defines the canonical color values for the entire application.
// Every styled element — output helpers, huh forms, spinners, tables —
// derives its colors from this palette.
//
// Values are drawn from the Charm color scheme so that huh forms and CLI
// output share a single visual language.
var Palette = struct {
	// Semantic colors.
	Primary lipgloss.TerminalColor // Titles, headings
	Accent  lipgloss.TerminalColor // Selectors, prompts, interactive elements
	Success lipgloss.TerminalColor // Confirmations, selected items
	Error   lipgloss.TerminalColor // Errors, validation failures
	Warning lipgloss.TerminalColor // Caution, dry-run indicators
	Muted   lipgloss.TerminalColor // Secondary text, descriptions
	NormalFg lipgloss.TerminalColor // Default foreground

	// Symbols.
	Check  string
	Cross  string
	Arrow  string
	Bullet string
	Dot    string
}{
	Primary:  lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7571F9"}, // Indigo
	Accent:   lipgloss.Color("#F780E2"),                                 // Fuchsia
	Success:  lipgloss.AdaptiveColor{Light: "#02BA84", Dark: "#02BF87"}, // Green
	Error:    lipgloss.AdaptiveColor{Light: "#FF4672", Dark: "#ED567A"}, // Red
	Warning:  lipgloss.AdaptiveColor{Light: "#FF8C00", Dark: "#FFA500"}, // Orange
	Muted:    lipgloss.AdaptiveColor{Light: "", Dark: "243"},            // Gray
	NormalFg: lipgloss.AdaptiveColor{Light: "235", Dark: "252"},

	Check:  "✓",
	Cross:  "✗",
	Arrow:  "→",
	Bullet: "•",
	Dot:    "·",
}

// FormTheme returns a huh theme built from the app palette. Use this for
// all huh forms so that interactive prompts match the rest of the CLI.
func FormTheme() *huh.Theme {
	t := huh.ThemeBase()

	t.Focused.Base = t.Focused.Base.BorderForeground(lipgloss.Color("238"))
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(Palette.Primary).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(Palette.Primary).Bold(true).MarginBottom(1)
	t.Focused.Directory = t.Focused.Directory.Foreground(Palette.Primary)
	t.Focused.Description = t.Focused.Description.Foreground(Palette.Muted)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(Palette.Error)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(Palette.Error)
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(Palette.Accent)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(Palette.Accent)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(Palette.Accent)
	t.Focused.Option = t.Focused.Option.Foreground(Palette.NormalFg)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(Palette.Accent)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(Palette.Success)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(Palette.Success).SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(Palette.Muted).SetString("• ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(Palette.NormalFg)
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FFFDF5"}).
		Background(Palette.Accent)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(Palette.NormalFg).
		Background(lipgloss.AdaptiveColor{Light: "252", Dark: "237"})

	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Palette.Success)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.
		Foreground(lipgloss.AdaptiveColor{Light: "248", Dark: "238"})
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Palette.Accent)

	t.Blurred = t.Focused
	t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}
