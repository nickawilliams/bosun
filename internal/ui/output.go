package ui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// preserveCaseMarker is the invisible sentinel PreserveCase prepends
// to opt a string out of the cli package's auto title-case transform.
// U+2060 (word joiner) is zero-width, never combines, and is vanishingly
// unlikely to occur naturally in a label — so leaking the marker would
// be both invisible and harmless.
const preserveCaseMarker = "⁠"

// TitleCase capitalizes the first rune of each space-separated word
// while leaving words that already contain an uppercase letter
// untouched. Acronyms ("API", "URL") and brand names ("GitHub") pass
// through verbatim; only fully-lowercase words get their first rune
// uppercased. Exposed so the cli layer can apply the same convention
// to form-field labels that breadcrumbs and card titles already use.
func TitleCase(s string) string { return titleCase(s) }

// PreserveCase wraps a string with an invisible sentinel so the
// cli's form-field constructors skip their auto title-case transform.
// Use for labels with casing that matters and shouldn't be normalized
// — e.g. ui.PreserveCase("API key") if you want exactly "API key"
// rather than "API Key". The sentinel is stripped before the string
// reaches the user.
func PreserveCase(s string) string { return preserveCaseMarker + s }

// StripPreserveCase removes the PreserveCase sentinel if present and
// reports whether it was. Form-field constructors use this to decide
// between applying TitleCase or rendering the label verbatim.
func StripPreserveCase(s string) (clean string, verbatim bool) {
	return strings.CutPrefix(s, preserveCaseMarker)
}

var (
	errorStyle   = lipgloss.NewStyle().Foreground(Palette.Error)
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

// EmptyState prints a muted "no data" marker shaped like a single
// tree leaf: " └── ✗ <text>". Substitutes for an empty Tree / Card /
// Plan so the timeline keeps a consistent leaf-row vocabulary —
// recessed last-branch connector + cross glyph + dimmed text —
// instead of a stray sentence. Always renders (TTY and raw) so
// piped callers see the marker too, the same way Tree.Print does.
func EmptyState(msg string, args ...any) {
	text := fmt.Sprintf(msg, args...)
	chrome := lipgloss.NewStyle().Foreground(Palette.Recessed)
	glyph := lipgloss.NewStyle().Foreground(Palette.Error)
	subtle := lipgloss.NewStyle().Foreground(Palette.Subtle)
	fmt.Print(spacerPrefix())
	fmt.Printf(" %s %s %s\n",
		chrome.Render("└──"),
		glyph.Render(Palette.Cross),
		subtle.Render(text),
	)
}

// SuccessLine prints a one-line confirmation row in the timeline:
// success glyph + spacing + the caller's pre-rendered content. Use
// when the standard CompleteValue / Saved shapes don't fit because
// the content needs embedded styling across multiple segments (e.g.,
// a sentence with both a key and a file path emphasized differently).
// Always renders so piped callers see the line too.
func SuccessLine(content string) {
	fmt.Print(spacerPrefix())
	glyph := lipgloss.NewStyle().Foreground(Palette.Success).Render(Palette.Check)
	fmt.Printf(" %s  %s\n", glyph, content)
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
