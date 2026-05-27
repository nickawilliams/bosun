package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// breadcrumb is the root-header hierarchy component. Owned by
// CardRoot via Card.breadcrumb. Non-root cards have no breadcrumb.
// Build via Card.Breadcrumb; render via RenderRow / RenderCompactRow.
type breadcrumb struct {
	segments []string // data segments (project, work context)
}

// Style policy for breadcrumb content.
var (
	dataSegmentStyle = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Success) }
	commandTailStyle = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary) }
	appNameStyle     = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Brand) }
	separatorStyle   = func() lipgloss.Style { return lipgloss.NewStyle().Bold(true).Foreground(Palette.Recessed) }
	ruleStyle        = func() lipgloss.Style { return lipgloss.NewStyle().Foreground(Palette.Recessed) }
)

// AddSegment appends a hierarchy data segment.
func (b *breadcrumb) AddSegment(s string) {
	if s == "" {
		return
	}
	b.segments = append(b.segments, s)
}

// RenderRow assembles the bottom row of the root box at the given
// inner box width. commandTail comes from the surrounding Card's
// title (split on " › "); the breadcrumb owns data segments.
// Returns the row including its trailing newline.
//
// Visual layout:
//
//	│  <segments> › <commandTail> ─────────────────────╯
func (b *breadcrumb) RenderRow(boxInner int, commandTail []string) string {
	const pad = " "
	rule := ruleStyle()

	if len(b.segments) == 0 && len(commandTail) == 0 {
		return fmt.Sprintf("%s%s  %s\n", pad,
			rule.Render("│"),
			rule.Render(strings.Repeat("─", boxInner-2)+"╯"))
	}

	dataStyle := dataSegmentStyle()
	cmdStyle := commandTailStyle()
	sep := separatorStyle()

	var styledSegs []string
	for _, seg := range b.segments {
		styledSegs = append(styledSegs, dataStyle.Render(titleCase(seg)))
	}
	for _, seg := range commandTail {
		styledSegs = append(styledSegs, cmdStyle.Render(titleCase(seg)))
	}
	full := strings.Join(styledSegs, sep.Render(" › "))

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

// RenderCompactRow assembles a single-line compact header.
// Includes "Bosun" as an explicit first segment and the version
// string at the right. commandPath is the FULL set of command
// segments (including the root "bosun" entry).
//
// Visual layout:
//
//	╭─ Bosun › Project › Status ──────── v0.1.0-dev
func (b *breadcrumb) RenderCompactRow(termWidth int, commandPath []string) string {
	const pad = " "
	rule := ruleStyle()

	dataStyle := dataSegmentStyle()
	cmdStyle := commandTailStyle()
	sep := separatorStyle()

	var styledSegs []string

	// Root segment ("Bosun") — brand color.
	if len(commandPath) > 0 {
		styledSegs = append(styledSegs, appNameStyle().Render(titleCase(commandPath[0])))
	}

	// Data segments (project, work context).
	for _, seg := range b.segments {
		styledSegs = append(styledSegs, dataStyle.Render(titleCase(seg)))
	}

	// Command tail (everything after root).
	if len(commandPath) > 1 {
		for _, seg := range commandPath[1:] {
			styledSegs = append(styledSegs, cmdStyle.Render(titleCase(seg)))
		}
	}

	crumb := strings.Join(styledSegs, sep.Render(" › "))

	versionStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	version := versionStyle.Render(AppVersion)
	versionWidth := lipgloss.Width(version)

	usedWidth := 1 + 2 + 1 + lipgloss.Width(crumb) + 1 + 1 + versionWidth + 1
	ruleLen := termWidth - usedWidth
	if ruleLen < 1 {
		ruleLen = 1
	}

	return fmt.Sprintf("%s%s %s %s %s \n", pad,
		rule.Render("╭─"),
		crumb,
		rule.Render(strings.Repeat("─", ruleLen)),
		version)
}
