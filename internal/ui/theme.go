package ui

import (
	"image/color"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// Palette defines the canonical color values for the entire application.
// Every styled element — output helpers, huh forms, spinners, tables —
// derives its colors from this palette.
var Palette = struct {
	// Semantic colors.
	Primary  color.Color // Titles, headings
	Accent   color.Color // Selectors, prompts, interactive elements
	Success  color.Color // Confirmations, selected items
	Error    color.Color // Errors, validation failures
	Warning  color.Color // Caution, dry-run indicators
	Muted    color.Color // Secondary text, descriptions
	NormalFg color.Color // Default foreground

	// Symbols.
	Check  string
	Cross  string
	Arrow  string
	Bullet string
	Dot    string
}{
	Primary:  lipgloss.Color("#7571F9"), // Indigo
	Accent:   lipgloss.Color("#F780E2"), // Fuchsia
	Success:  lipgloss.Color("#02BF87"), // Green
	Error:    lipgloss.Color("#ED567A"), // Red
	Warning:  lipgloss.Color("#FFA500"), // Orange
	Muted:    lipgloss.Color("243"),     // Gray
	NormalFg: lipgloss.Color("252"),

	Check:  "✓",
	Cross:  "✗",
	Arrow:  "→",
	Bullet: "•",
	Dot:    "·",
}

// BosunTheme implements huh.Theme for use with huh forms.
type BosunTheme struct{}

// Theme returns styled huh Styles built from the app palette.
func (BosunTheme) Theme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)

	// Between fields in a multi-field group, huh inserts a
	// FieldSeparator on its own line. The default is a blank "\n\n"
	// which breaks the timeline spine. Use an UNSTYLED "\n │\n" so
	// a bare │ sits on its own row between fields without lipgloss
	// padding trailing whitespace into the next field's margin.
	// The bar is recolored to the recessed timeline gray by
	// NewTimelineLayout — see form_layout.go for the rationale.
	t.FieldSeparator = lipgloss.NewStyle().SetString("\n │\n")

	// Align huh's focused form with the card timeline: 1 space of
	// left margin, a normal-weight │ border in the accent color,
	// and 2 spaces of inner padding. Callers that want a "?" glyph
	// on the first row should print a CardInput title card before
	// invoking the form; the form itself only draws the connector,
	// which matches the CardInput card's own connector color.
	t.Focused.Base = lipgloss.NewStyle().
		MarginLeft(1).
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(Palette.Accent).
		PaddingLeft(2)
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
		Foreground(lipgloss.Color("#FFFDF5")).
		Background(Palette.Accent)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(Palette.NormalFg).
		Background(lipgloss.Color("237"))

	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Palette.Success)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.
		Foreground(lipgloss.Color("238"))
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Palette.Accent)

	t.Blurred = t.Focused
	// Blurred (inactive) fields keep a visible left gutter in the
	// recessed timeline color so the whole form reads as a single
	// continuous card, with the fuchsia accent only marking the
	// one row receiving input.
	t.Blurred.Base = t.Focused.Base.BorderForeground(cardConnectorColor)
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	// Help footer: keys + descriptions in recessed muted gray so
	// the shortcut hints sit quietly beneath the active prompt
	// without competing with the card timeline above. Indented
	// with a left margin so it aligns under the prompt content,
	// matching the 1-space outer pad + 1-col border + 2-col inner
	// padding used by the focused card.
	helpKey := lipgloss.NewStyle().Foreground(Palette.Muted)
	helpDesc := lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	helpSep := lipgloss.NewStyle().Foreground(lipgloss.Color("237"))
	t.Help.ShortKey = helpKey
	t.Help.ShortDesc = helpDesc
	t.Help.ShortSeparator = helpSep
	t.Help.Ellipsis = helpSep
	t.Help.FullKey = helpKey
	t.Help.FullDesc = helpDesc
	t.Help.FullSeparator = helpSep

	return t
}

// FormTheme returns the app's huh Theme.
func FormTheme() huh.Theme {
	return BosunTheme{}
}
