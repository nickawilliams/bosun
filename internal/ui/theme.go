package ui

import (
	"fmt"
	"image/color"
	"os"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// palette holds the canonical color values for the entire application.
// Every styled element — output helpers, huh forms, spinners, tables —
// derives its colors from this struct.
type palette struct {
	// Semantic colors.
	Primary   color.Color // Titles, headings
	Secondary color.Color // Breadcrumb root, secondary headings
	Accent    color.Color // Selectors, prompts, interactive elements
	Success  color.Color // Confirmations, selected items
	Error    color.Color // Errors, validation failures
	Warning  color.Color // Caution, dry-run indicators
	Muted    color.Color // Secondary text, descriptions
	NormalFg color.Color // Default foreground

	// Chrome colors — structural UI elements.
	Recessed color.Color // Timeline spine, blurred button bg, help separator
	Border   color.Color // Panel/table borders, input placeholder
	Subtle   color.Color // Help description text
	ButtonFg color.Color // Focused button foreground

	// Symbols.
	Check  string
	Cross  string
	Arrow  string
	Bullet string
	Dot    string
}

// Palette is the active color palette. Swapped by ApplyColorMode before
// any rendering occurs; read freely afterward (single-goroutine init).
var Palette = defaultPalette()

// DisplayMode controls the density of rendered output.
type DisplayMode int

const (
	DisplayCompact     DisplayMode = iota // No extra spacing (default).
	DisplayComfy                    // Breathing room between cards.
	DisplayVerbose                        // Reserved: richer incremental output.
)

// displayMode is the active display mode. Set by ApplyDisplayMode before
// any rendering occurs; read freely afterward (single-goroutine init).
var displayMode = DisplayCompact

// ApplyDisplayMode sets the active display mode from a config string.
// Must be called after config loads and before any rendering.
func ApplyDisplayMode(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "comfy":
		displayMode = DisplayComfy
	case "verbose":
		displayMode = DisplayVerbose
	default:
		displayMode = DisplayCompact
	}
}

// IsComfy reports whether the display mode adds breathing room.
// Returns true for both comfortable and verbose modes.
func IsComfy() bool {
	return displayMode >= DisplayComfy
}

// displayPadding returns extra vertical whitespace to insert after a
// non-timeline block (e.g. Panel) when the display mode calls for
// breathing room.
func displayPadding() string {
	if displayMode >= DisplayComfy {
		return "\n"
	}
	return ""
}

// comfyBreak is set after a timeline card prints to signal that the
// next card should be preceded by a connector line. The connector is
// emitted as a leading prefix so the last card in a run never leaves
// a dangling │.
var comfyBreak bool

// BreakTimeline requests a connector-line break before the next
// card in comfy mode. Use when the next card follows output that
// didn't go through the normal Print path (e.g. an interrupted
// huh form).
func BreakTimeline() {
	comfyBreak = true
}

// FlushBreak prints and clears a pending comfy break immediately.
// Use before non-card output (e.g., huh forms) that won't call
// comfyPrefix() itself.
func FlushBreak() {
	fmt.Print(comfyPrefix())
}

// ClearBreak discards a pending comfy connector without printing
// it. Use to create a visual gap between unrelated timeline
// sections where the │ connector would be misleading.
func ClearBreak() {
	comfyBreak = false
}

// BeginTimeline prints a leading blank line in comfy mode to
// separate the timeline from the shell prompt above.
func BeginTimeline() {
	if IsComfy() {
		fmt.Println()
	}
}

// EndTimeline prints a trailing blank line in comfy mode to close
// the visual timeline with clean whitespace.
func EndTimeline() {
	if IsComfy() {
		fmt.Println()
	}
}

// comfyPrefix returns (and clears) a pending connector-line prefix
// for comfy-mode breathing room between timeline cards.
func comfyPrefix() string {
	if !comfyBreak || !IsComfy() {
		comfyBreak = false
		return ""
	}
	comfyBreak = false
	conn := lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
	return " " + conn + "\n"
}

func defaultPalette() palette {
	return palette{
		Primary:   lipgloss.Color("#7571F9"), // Indigo
		Secondary: lipgloss.Color("#9997CC"), // Desaturated indigo
		Accent:    lipgloss.Color("#F780E2"), // Fuchsia
		Success:  lipgloss.Color("#02BF87"), // Green
		Error:    lipgloss.Color("#ED567A"), // Red
		Warning:  lipgloss.Color("#FFA500"), // Orange
		Muted:    lipgloss.Color("243"),     // Gray
		NormalFg: lipgloss.Color("252"),
		Recessed: lipgloss.Color("237"),
		Border:   lipgloss.Color("238"),
		Subtle:   lipgloss.Color("239"),
		ButtonFg: lipgloss.Color("#FFFDF5"),

		Check:  "✓",
		Cross:  "✗",
		Arrow:  "→",
		Bullet: "•",
		Dot:    "·",
	}
}

func ansiPalette() palette {
	return palette{
		Primary:   lipgloss.BrightBlue,
		Secondary: lipgloss.Blue,
		Accent:    lipgloss.BrightMagenta,
		Success:  lipgloss.Green,
		Error:    lipgloss.Red,
		Warning:  lipgloss.Yellow,
		Muted:    lipgloss.BrightBlack,
		NormalFg: lipgloss.White,
		Recessed: lipgloss.BrightBlack,
		Border:   lipgloss.BrightBlack,
		Subtle:   lipgloss.BrightBlack,
		ButtonFg: lipgloss.BrightWhite,

		Check: "✓", Cross: "✗", Arrow: "→", Bullet: "•", Dot: "·",
	}
}

func noColorPalette() palette {
	nc := lipgloss.NoColor{}
	return palette{
		Primary: nc, Secondary: nc, Accent: nc, Success: nc, Error: nc, Warning: nc,
		Muted: nc, NormalFg: nc, Recessed: nc, Border: nc, Subtle: nc,
		ButtonFg: nc,

		Check: "✓", Cross: "✗", Arrow: "→", Bullet: "•", Dot: "·",
	}
}

// ApplyColorMode sets the active palette based on the given mode string
// and rebuilds all cached package-level styles. Must be called after
// config loads and before any rendering (i.e. in PersistentPreRunE).
func ApplyColorMode(mode string) {
	// NO_COLOR env var (https://no-color.org) acts as implicit "none"
	// unless the user explicitly configured a color mode.
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor && mode == "" {
		mode = "none"
	}

	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "ansi":
		Palette = ansiPalette()
	case "none":
		Palette = noColorPalette()
	default:
		Palette = defaultPalette()
	}

	rebuildStyles()
}

// rebuildStyles refreshes every package-level style var that captured
// palette values at init time. Called by ApplyColorMode.
func rebuildStyles() {
	// output.go
	successStyle = lipgloss.NewStyle().Foreground(Palette.Success)
	errorStyle = lipgloss.NewStyle().Foreground(Palette.Error)
	warningStyle = lipgloss.NewStyle().Foreground(Palette.Warning)
	mutedStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
	primaryStyle = lipgloss.NewStyle().Foreground(Palette.Primary)


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
		Foreground(Palette.ButtonFg).
		Background(Palette.Accent)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(Palette.NormalFg).
		Background(Palette.Recessed)

	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(Palette.Success)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.
		Foreground(Palette.Border)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(Palette.Accent)

	t.Blurred = t.Focused
	// Blurred (inactive) fields keep a visible left gutter in the
	// recessed timeline color so the whole form reads as a single
	// continuous card, with the fuchsia accent only marking the
	// one row receiving input.
	t.Blurred.Base = t.Focused.Base.BorderForeground(Palette.Recessed)
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
	helpDesc := lipgloss.NewStyle().Foreground(Palette.Subtle)
	helpSep := lipgloss.NewStyle().Foreground(Palette.Recessed)
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
