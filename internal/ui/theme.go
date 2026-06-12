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
//
// Two parallel color vocabularies live here. They alias the same
// underlying color values; the names communicate intent at the call
// site, and `grep` finds the right places when you need to audit one
// context vs the other.
//
//	Severity colors (event contexts — Doctor checks, action results):
//	  Success / Warning / Error / Info / Muted — "what happened"
//
//	Resolution-role colors (state contexts — Status rows, lifecycle
//	indicators):
//	  RoleOpen / RoleDone / RoleClosed / RoleAttention / RoleInFlight
//	  / RoleNeutral — "where this aspect is right now"
//
// See state_grammar.go for the grammar that ties these to glyph
// choices and decides which vocabulary applies in which context.
type palette struct {
	// Semantic colors.
	Primary   color.Color // Titles, headings
	Secondary color.Color // Secondary headings
	Brand     color.Color // Application name in breadcrumbs
	LogoTop   color.Color // Logo gradient start (top line)
	LogoBottom color.Color // Logo gradient end (bottom line)
	Accent    color.Color // Selectors, prompts, interactive elements
	Info     color.Color // Informational, non-actionable signals
	Success  color.Color // Confirmations, selected items
	Error    color.Color // Errors, validation failures
	Warning  color.Color // Caution, dry-run indicators
	Muted    color.Color // Secondary text, descriptions
	NormalFg color.Color // Default foreground

	// Resolution-role colors — for state-context rows (see
	// state_grammar.go). Alias the severity colors but read as
	// intent at the call site. Use these when the row describes
	// "what this aspect currently is", not "what just happened".
	RoleOpen      color.Color // Active / healthy aspect       (= Success, green)
	RoleDone      color.Color // Terminal positive resolution  (= Primary, purple)
	RoleClosed    color.Color // Terminal negative resolution  (= Error, red)
	RoleAttention color.Color // Needs intervention            (= Warning, yellow)
	RoleInFlight  color.Color // Transitioning right now       (= Info, blue)
	RoleNeutral   color.Color // Unknown / unset / not started (= Muted, gray)

	// Keyword marks identifier-shaped tokens that should pop out of
	// surrounding prose — repo names, service names, branch names,
	// commands, config keys. Render through ui.Keyword(s) rather
	// than styling ad hoc so the treatment stays uniform. (= Primary)
	Keyword color.Color

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

// compactHeader controls whether the root card renders as a compact
// single-line breadcrumb instead of the full ASCII logo box.
var compactHeader bool

// SetCompactHeader sets the compact header flag directly from a bool
// config value.
func SetCompactHeader(v bool) {
	compactHeader = v
}

// IsCompactHeader reports whether the root card should render as a
// single-line breadcrumb header instead of the full logo box.
func IsCompactHeader() bool {
	return compactHeader
}

// displayPadding returns extra vertical whitespace to insert after a
// non-timeline block (e.g. Panel) for breathing room.
func displayPadding() string {
	return "\n"
}

// needsSpacer is set after a timeline card prints to signal that
// the next card should be preceded by a connector line (" │\n").
// The connector is emitted as a leading prefix so the last card
// in a run never leaves a dangling │.
var needsSpacer bool

// RequestSpacer requests a connector-line spacer before the next
// card. Use when the next card follows output that didn't go
// through the normal Print path (e.g. an interrupted huh form).
func RequestSpacer() {
	needsSpacer = true
}

// FlushSpacer prints and clears a pending spacer immediately.
// Use before non-card output (e.g., huh forms) that won't call
// spacerPrefix() itself.
func FlushSpacer() {
	fmt.Print(spacerPrefix())
}

// ClearSpacer discards a pending spacer without printing it.
// Use to suppress the connector between unrelated timeline
// sections where the │ would be misleading.
func ClearSpacer() {
	needsSpacer = false
}

// BeginTimeline prints a leading blank line to separate the
// timeline from the shell prompt above.
func BeginTimeline() {
	fmt.Println()
}

// EndTimeline prints a trailing blank line to close the visual
// timeline with clean whitespace.
func EndTimeline() {
	fmt.Println()
}

// Divider prints a muted horizontal rule between cards while
// preserving the timeline spine. The line is indented to align
// with card body content (under the spine + 2-space gap), so the
// │ connector remains visible and continuous through the section
// break. Suppressed in raw mode.
func Divider() {
	if IsRaw() {
		return
	}
	// Emit any pending comfy connector first so the divider has a
	// blank │ row above it in comfy mode.
	fmt.Print(spacerPrefix())

	style := lipgloss.NewStyle().Foreground(Palette.Recessed)
	width := max(TermWidth()-2, 10)
	fmt.Println(" " + style.Render("├"+strings.Repeat("─", width)))

	// Trigger a │ row above the next card so the divider has
	// breathing room beneath it too.
	needsSpacer = true
}

// spacerPrefix returns a connector-line spacer (" │\n") if one is
// pending, then re-arms the flag so the next consumer gets one too.
// Returns "" on the first call (before any output has set the flag).
// Tight cards call ClearSpacer() to suppress the next spacer.
func spacerPrefix() string {
	if !needsSpacer {
		needsSpacer = true // arm for next time
		return ""
	}
	conn := lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
	return " " + conn + "\n"
}

// lerpColors returns n colors interpolated linearly from a to b.
// For n <= 1, returns []color.Color{a}.
func lerpColors(a, b color.Color, n int) []color.Color {
	if n <= 1 {
		return []color.Color{a}
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	colors := make([]color.Color, n)
	for i := range n {
		t := float64(i) / float64(n-1)
		colors[i] = lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
			uint8(float64(ar>>8)*(1-t)+float64(br>>8)*t),
			uint8(float64(ag>>8)*(1-t)+float64(bg>>8)*t),
			uint8(float64(ab>>8)*(1-t)+float64(bb>>8)*t),
		))
	}
	return colors
}

func defaultPalette() palette {
	p := palette{
		Primary:   lipgloss.Color("#7571F9"), // Indigo
		Secondary: lipgloss.Color("#9997CC"), // Desaturated indigo
		Brand:      lipgloss.Color("#9997CC"), // Desaturated indigo (app name in breadcrumbs)
		LogoTop:    lipgloss.Color("#7571F9"), // Bright indigo (logo gradient start)
		LogoBottom: lipgloss.Color("#9997CC"), // Desaturated indigo (logo gradient end)
		Accent:    lipgloss.Color("#F780E2"), // Fuchsia
		Info:     lipgloss.Color("#5DA9F8"), // Sky blue
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
	applyRoleAliases(&p)
	return p
}

func ansiPalette() palette {
	p := palette{
		Primary:   lipgloss.BrightBlue,
		Secondary: lipgloss.Blue,
		Brand:      lipgloss.Blue,
		LogoTop:    lipgloss.BrightBlue,
		LogoBottom: lipgloss.Blue,
		Accent:    lipgloss.BrightMagenta,
		Info:     lipgloss.Cyan,
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
	applyRoleAliases(&p)
	return p
}

func noColorPalette() palette {
	nc := lipgloss.NoColor{}
	p := palette{
		Primary: nc, Secondary: nc, Brand: nc, LogoTop: nc, LogoBottom: nc, Accent: nc, Info: nc, Success: nc, Error: nc, Warning: nc,
		Muted: nc, NormalFg: nc, Recessed: nc, Border: nc, Subtle: nc,
		ButtonFg: nc,

		Check: "✓", Cross: "✗", Arrow: "→", Bullet: "•", Dot: "·",
	}
	applyRoleAliases(&p)
	return p
}

// applyRoleAliases populates the palette's resolution-role color
// fields from the severity colors. Kept in one place so any future
// re-wiring (e.g., decoupling RoleDone from Primary) lives in a
// single spot rather than scattered across each palette constructor.
func applyRoleAliases(p *palette) {
	p.RoleOpen = p.Success
	p.RoleDone = p.Primary
	p.RoleClosed = p.Error
	p.RoleAttention = p.Warning
	p.RoleInFlight = p.Info
	p.RoleNeutral = p.Muted
	p.Keyword = p.Primary
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
	errorStyle = lipgloss.NewStyle().Foreground(Palette.Error)
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
