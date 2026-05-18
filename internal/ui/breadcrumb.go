package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// breadcrumb is the root-header hierarchy + trailing-slot component.
// Owned by CardRoot via Card.breadcrumb. Non-root cards have no
// breadcrumb. Build via the Card builder methods (Card.Breadcrumb,
// the squish glue calling SetTrailing); render via RenderRow.
//
// Styles are owned by the component (see package vars below). Data
// segments, command tail, and the trailing slot title each have
// their own fixed style derived from Palette colors. Callers don't
// configure styling — the breadcrumb owns its visual identity.
type breadcrumb struct {
	segments      []string // data segments (hierarchy), "Bosun" implicit
	trailingTitle string   // squished card's title; "" when empty
	trailingGlyph string   // squished card's glyph; "" when empty
	suppressGlyph bool     // honor HideAbsorbedGlyph from source
}

// Style policy for breadcrumb content. Each category has its own
// fixed style; callers don't configure these. Derived from Palette
// colors so theme switches via ApplyColorMode propagate naturally.
var (
	dataSegmentStyle   = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Success) }
	commandTailStyle   = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary) }
	trailingTitleStyle = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary) }
	separatorStyle     = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Recessed) }
	ruleStyle          = func() lipgloss.Style { return lipgloss.NewStyle().Foreground(Palette.Recessed) }
)

// AddSegment appends a hierarchy data segment.
func (b *breadcrumb) AddSegment(s string) {
	if s == "" {
		return
	}
	b.segments = append(b.segments, s)
}

// SetTrailing fills the trailing slot from a squished card. Pass
// empty strings to clear. suppress controls whether the glyph
// appears (mirrors HideAbsorbedGlyph on the source card) — when
// true, the glyph is dropped regardless of what was passed.
func (b *breadcrumb) SetTrailing(title, glyph string, suppress bool) {
	b.trailingTitle = title
	if suppress {
		b.trailingGlyph = ""
	} else {
		b.trailingGlyph = glyph
	}
	b.suppressGlyph = suppress
}

// hasTrailing reports whether the trailing slot has any content.
func (b *breadcrumb) hasTrailing() bool {
	return b.trailingTitle != "" || b.trailingGlyph != ""
}

// copy returns a shallow copy suitable for the squish mechanism's
// "extended root" pattern: same segments slice (read-only after
// construction), but trailing slot can be independently mutated.
func (b *breadcrumb) copy() *breadcrumb {
	if b == nil {
		return &breadcrumb{}
	}
	dup := *b
	return &dup
}

// LineCount reports how many terminal lines RenderRow produces.
// Currently always 1 (no wrapping in either logo or future compact
// header mode). The squish path's partial-rewrite fast path falls
// back to full-erase when this returns >1.
func (b *breadcrumb) LineCount(boxInner int, commandTail []string) int {
	s := b.RenderRow(boxInner, commandTail)
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
}

// RenderRow assembles the bottom row of the root box at the given
// inner box width (number of cells between the side borders).
// commandTail comes from the surrounding Card's title (split on
// " › "); the breadcrumb owns segments and trailing, but not the
// command-path. Returns the row including its trailing newline.
//
// Visual layout:
//
//	│  <segments> › <commandTail> › <glyph> <title> ───╯
//	   └ data segments┘└ command tail ┘└ trailing slot ┘
//
// The trailing-slot separator (› before the slot) only renders
// when the slot has content. Empty breadcrumb (no segments, no
// command tail) renders just rule chars and the closing corner.
func (b *breadcrumb) RenderRow(boxInner int, commandTail []string) string {
	const pad = " "
	rule := ruleStyle()

	dataSegs := b.segments

	if len(dataSegs) == 0 && len(commandTail) == 0 && !b.hasTrailing() {
		return fmt.Sprintf("%s%s  %s\n", pad,
			rule.Render("│"),
			rule.Render(strings.Repeat("─", boxInner-2)+"╯"))
	}

	dataStyle := dataSegmentStyle()
	cmdStyle := commandTailStyle()
	sep := separatorStyle()

	// Build the styled main breadcrumb (data + command tail) joined by ›.
	var styledSegs []string
	for _, seg := range dataSegs {
		styledSegs = append(styledSegs, dataStyle.Render(titleCase(seg)))
	}
	for _, seg := range commandTail {
		styledSegs = append(styledSegs, cmdStyle.Render(titleCase(seg)))
	}
	mainCrumb := strings.Join(styledSegs, sep.Render(" › "))

	// Append the trailing slot if present, with its own › separator.
	trailing := ""
	if b.hasTrailing() {
		var parts []string
		if b.trailingGlyph != "" {
			parts = append(parts, b.trailingGlyph)
		}
		if b.trailingTitle != "" {
			parts = append(parts, trailingTitleStyle().Render(b.trailingTitle))
		}
		trailing = sep.Render(" › ") + strings.Join(parts, " ")
	}

	full := mainCrumb + trailing

	prefix := ""
	prefixWidth := 0
	if BreadcrumbPrefix != "" {
		prefix = rule.Render(BreadcrumbPrefix) + " "
		prefixWidth = lipgloss.Width(prefix)
	}
	postfix := " "
	postfixWidth := 1
	if BreadcrumbPostfix != "" {
		postfix = " " + rule.Render(BreadcrumbPostfix) + " "
		postfixWidth = lipgloss.Width(BreadcrumbPostfix) + 2
	}

	ruleLen := boxInner - 2 - prefixWidth - lipgloss.Width(full) - postfixWidth
	if ruleLen < 1 {
		ruleLen = 1
	}
	return fmt.Sprintf("%s%s  %s%s%s%s\n", pad,
		rule.Render("│"),
		prefix,
		full,
		postfix,
		rule.Render(strings.Repeat("─", ruleLen)+"╯"))
}

// RenderCompactRow assembles a single-line compact header that
// replaces the multi-line logo box. Includes "Bosun" as an explicit
// first segment (since the logo art no longer stands in for it) and
// the version string at the right. commandPath is the FULL set of
// command segments (including the root "bosun" entry, unlike
// RenderRow which expects it dropped).
//
// Visual layout:
//
//	╭─ Bosun › Project › EX-1234 › Status ──────── v0.1.0-dev
//	   └ data ┘└ command path ──────┘ └ trailing ┘  └ version ┘
func (b *breadcrumb) RenderCompactRow(termWidth int, commandPath []string) string {
	const pad = " "
	rule := ruleStyle()

	dataStyle := dataSegmentStyle()
	cmdStyle := commandTailStyle()
	sep := separatorStyle()

	// Build the full breadcrumb: "Bosun" root + data segments + command tail.
	// The root segment (commandPath[0], typically "bosun") uses command style;
	// data segments use data style; command tail uses command style.
	var styledSegs []string

	// Root segment ("Bosun") — always present, uses command style.
	if len(commandPath) > 0 {
		styledSegs = append(styledSegs, cmdStyle.Render(titleCase(commandPath[0])))
	}

	// Data segments (project, issue, etc.) — data style.
	for _, seg := range b.segments {
		styledSegs = append(styledSegs, dataStyle.Render(titleCase(seg)))
	}

	// Command tail (everything after the root) — command style.
	if len(commandPath) > 1 {
		for _, seg := range commandPath[1:] {
			styledSegs = append(styledSegs, cmdStyle.Render(titleCase(seg)))
		}
	}

	// Trailing slot (squished card title + glyph, if any).
	if b.hasTrailing() {
		var parts []string
		if b.trailingGlyph != "" {
			parts = append(parts, b.trailingGlyph)
		}
		if b.trailingTitle != "" {
			parts = append(parts, trailingTitleStyle().Render(b.trailingTitle))
		}
		styledSegs = append(styledSegs, strings.Join(parts, " "))
	}

	crumb := strings.Join(styledSegs, sep.Render(" › "))

	// Version at the right edge.
	versionStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	version := versionStyle.Render(AppVersion)
	versionWidth := lipgloss.Width(version)

	// Layout: pad(1) + ╭─(2) + space(1) + crumb + space(1) + rule + space(1) + version
	usedWidth := 1 + 2 + 1 + lipgloss.Width(crumb) + 1 + 1 + versionWidth
	ruleLen := termWidth - usedWidth
	if ruleLen < 1 {
		ruleLen = 1
	}

	return fmt.Sprintf("%s%s %s %s %s\n", pad,
		rule.Render("╭─"),
		crumb,
		rule.Render(strings.Repeat("─", ruleLen)),
		version)
}
