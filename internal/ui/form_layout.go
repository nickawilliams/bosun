package ui

import (
	"regexp"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// timelineLayout wraps huh's default layout to post-process the
// rendered form string, recoloring the field separator bars in our
// recessed timeline gray.
//
// Why this exists: huh's only knob for styling the between-fields
// gap is Theme.FieldSeparator, a lipgloss.Style. Rendering a
// multi-line styled string through lipgloss runs it through
// alignTextHorizontal, which pads every line with trailing spaces
// styled by the style's whitespace color. Those trailing spaces
// bleed into the next field's MarginLeft and shift its border by
// two columns. An unstyled separator avoids the visible offset but
// renders the bar in the default terminal color instead of our
// timeline gray.
//
// The layout's View() returns a plain string that Form.View() wraps
// with the outer Base style, so we can safely post-process it
// without interfering with focus, keyboard handling, or the viewport.
// We match the separator row — a bare " │" at column 1 followed by
// alignment padding — with a regex and wrap just the │ character
// with ANSI foreground codes. The field content rows are never
// matched because they have styled content after their border.
type timelineLayout struct {
	inner huh.Layout
}

// separatorRowPattern matches the separator row emitted by an
// unstyled FieldSeparator: a newline, a single space, the │
// character, then any number of trailing spaces produced by
// lipgloss's horizontal alignment, then another newline. The
// trailing-space run is what makes this impossible to match with
// strings.Replace — its length depends on the widest line in the
// rendered form, which varies per frame.
var separatorRowPattern = regexp.MustCompile(`\n │( *)\n`)

// NewTimelineLayout returns a huh.Layout that wraps LayoutDefault
// and recolors the field separator bars. Pair with
// Theme.FieldSeparator = lipgloss.NewStyle().SetString("\n │\n").
func NewTimelineLayout() huh.Layout {
	return timelineLayout{inner: huh.LayoutDefault}
}

func (l timelineLayout) View(f *huh.Form) string {
	raw := l.inner.View(f)
	styled := lipgloss.NewStyle().Foreground(Palette.Recessed).Render("│")
	return separatorRowPattern.ReplaceAllString(raw, "\n "+styled+"${1}\n")
}

func (l timelineLayout) GroupWidth(f *huh.Form, g *huh.Group, w int) int {
	return l.inner.GroupWidth(f, g, w)
}
