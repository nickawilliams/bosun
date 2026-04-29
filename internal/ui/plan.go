package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// PlanOp represents what kind of change a plan item describes.
type PlanOp int

const (
	PlanCreate   PlanOp = iota // + create new resource
	PlanModify                 // ~ modify existing resource
	PlanDestroy                // - destroy resource
	PlanNoChange               // = no change (already exists)
	PlanDetail                 // + informational sub-item (not counted in summaries)
)

// PlanItem describes a single action in a plan. The four core fields (Op,
// Action, Type, Name) form the identity of the change; Detail is
// supplementary human-readable context.
type PlanItem struct {
	Op     PlanOp
	Action string // operation noun: "deploy", "branch", "notify"
	Type   string // subject category: "repo", "env", "channel", "issue"
	Name   string // subject identifier: "api", "brave-falcon", "#reviews"
	Detail string // free-form qualifier: transition, state, description
}

// Plan collects planned actions and renders them as a diff-style list.
type Plan struct {
	items []PlanItem
}

// NewPlan creates a new empty plan.
func NewPlan() *Plan {
	return &Plan{}
}

// Add appends a plan item.
func (p *Plan) Add(op PlanOp, action, subjectType, name, detail string) *Plan {
	p.items = append(p.items, PlanItem{Op: op, Action: action, Type: subjectType, Name: name, Detail: detail})
	return p
}

// IsEmpty returns true if the plan has no items.
func (p *Plan) IsEmpty() bool {
	return len(p.items) == 0
}

// HasChanges returns true if the plan has any actionable items.
func (p *Plan) HasChanges() bool {
	for _, item := range p.items {
		if item.Op != PlanNoChange && item.Op != PlanDetail {
			return true
		}
	}
	return false
}

// Render returns the plan as a styled string for display in the timeline.
func (p *Plan) Render() string {
	if len(p.items) == 0 {
		return ""
	}

	var b strings.Builder

	// Plan heading as a card-style line.
	headingStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	connStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)

	glyphStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	summary := p.Summary()
	fmt.Fprintf(&b, " %s  %s %s\n", glyphStyle.Render(cardGlyphInfo), headingStyle.Render("Plan:"), summary)

	widths := p.columnWidths()

	for _, item := range p.items {
		fmt.Fprintf(&b, " %s    %s\n",
			connStyle.Render("│"),
			renderPlanRow(item, widths),
		)
	}

	return b.String()
}

// Print writes the plan to stdout.
func (p *Plan) Print() {
	fmt.Print(comfyPrefix() + p.Render())
	comfyBreak = true
}

// PrintRewindable writes the plan to stdout and returns a function that
// erases it (same pattern as Card.PrintRewindable).
func (p *Plan) PrintRewindable() func() {
	prev := comfyBreak
	rendered := comfyPrefix() + p.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	comfyBreak = true
	return func() {
		if lines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
		comfyBreak = prev
	}
}

// RenderItems returns just the formatted action lines without heading or
// timeline spine. Suitable for embedding as content in another component.
func (p *Plan) RenderItems() string {
	if len(p.items) == 0 {
		return ""
	}

	widths := p.columnWidths()
	var b strings.Builder
	for _, item := range p.items {
		fmt.Fprintf(&b, "  %s\n", renderPlanRow(item, widths))
	}
	return strings.TrimRight(b.String(), "\n")
}

// Summary returns the count line: "1 unchanged, 2 to create, 1 to update"
func (p *Plan) Summary() string {
	counts := map[PlanOp]int{}
	for _, item := range p.items {
		counts[item.Op]++
	}

	var parts []string

	createStyle := lipgloss.NewStyle().Foreground(Palette.Success)
	modifyStyle := lipgloss.NewStyle().Foreground(Palette.Warning)
	destroyStyle := lipgloss.NewStyle().Foreground(Palette.Error)
	unchangedStyle := lipgloss.NewStyle().Foreground(Palette.Muted)

	if n := counts[PlanCreate] + counts[PlanDetail]; n > 0 {
		parts = append(parts, createStyle.Render(fmt.Sprintf("%d to create", n)))
	}
	if n := counts[PlanModify]; n > 0 {
		parts = append(parts, modifyStyle.Render(fmt.Sprintf("%d to update", n)))
	}
	if n := counts[PlanDestroy]; n > 0 {
		parts = append(parts, destroyStyle.Render(fmt.Sprintf("%d to destroy", n)))
	}
	if n := counts[PlanNoChange]; n > 0 {
		parts = append(parts, unchangedStyle.Render(fmt.Sprintf("%d unchanged", n)))
	}

	return strings.Join(parts, ", ")
}

// SummaryPastTense returns "2 created, 1 updated" — for the success state.
func (p *Plan) SummaryPastTense() string {
	counts := map[PlanOp]int{}
	for _, item := range p.items {
		counts[item.Op]++
	}

	var parts []string

	createStyle := lipgloss.NewStyle().Foreground(Palette.Success)
	modifyStyle := lipgloss.NewStyle().Foreground(Palette.Warning)
	destroyStyle := lipgloss.NewStyle().Foreground(Palette.Error)
	unchangedStyle := lipgloss.NewStyle().Foreground(Palette.Muted)

	if n := counts[PlanCreate] + counts[PlanDetail]; n > 0 {
		parts = append(parts, createStyle.Render(fmt.Sprintf("%d created", n)))
	}
	if n := counts[PlanModify]; n > 0 {
		parts = append(parts, modifyStyle.Render(fmt.Sprintf("%d updated", n)))
	}
	if n := counts[PlanDestroy]; n > 0 {
		parts = append(parts, destroyStyle.Render(fmt.Sprintf("%d destroyed", n)))
	}
	if n := counts[PlanNoChange]; n > 0 {
		parts = append(parts, unchangedStyle.Render(fmt.Sprintf("%d unchanged", n)))
	}

	return strings.Join(parts, ", ")
}

// SummaryPartial returns a mixed-tense summary for partial application.
func (p *Plan) SummaryPartial(succeeded, failed int) string {
	// Detail items are display-only and always succeed with the parent action.
	detailCount := 0
	for _, item := range p.items {
		if item.Op == PlanDetail {
			detailCount++
		}
	}
	succeeded += detailCount

	failStyle := lipgloss.NewStyle().Foreground(Palette.Error)
	successStyle := lipgloss.NewStyle().Foreground(Palette.Success)

	var parts []string
	if failed > 0 {
		parts = append(parts, failStyle.Render(fmt.Sprintf("%d failed", failed)))
	}
	if succeeded > 0 {
		parts = append(parts, successStyle.Render(fmt.Sprintf("%d applied", succeeded)))
	}

	return strings.Join(parts, ", ")
}

// RenderItemLines returns the formatted action lines as a slice for reuse
// by PlanCard. Each line includes the symbol, action, type, name, and detail
// but no spine or heading.
func (p *Plan) RenderItemLines() []string {
	if len(p.items) == 0 {
		return nil
	}

	widths := p.columnWidths()
	var lines []string
	for _, item := range p.items {
		lines = append(lines, "  "+renderPlanRow(item, widths))
	}
	return lines
}

// planColumnWidths captures the width of each variable-length column.
type planColumnWidths struct {
	action int
	typ    int
	name   int
}

// columnWidths returns the max widths for the action, type, and name columns
// across all plan items. Used to align the diff-style display.
func (p *Plan) columnWidths() planColumnWidths {
	var w planColumnWidths
	for _, item := range p.items {
		if len(item.Action) > w.action {
			w.action = len(item.Action)
		}
		if len(item.Type) > w.typ {
			w.typ = len(item.Type)
		}
		if len(item.Name) > w.name {
			w.name = len(item.Name)
		}
	}
	return w
}

// renderPlanRow renders one PlanItem as the symbol + action + type + name +
// detail, padded to the given column widths. NoChange items render fully
// muted to convey "no work to do."
func renderPlanRow(item PlanItem, w planColumnWidths) string {
	symbol, symbolStyle := planSymbol(item.Op)
	actionStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
	typeStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	nameStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	detailStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)

	if item.Op == PlanNoChange {
		actionStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		typeStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		nameStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		detailStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
	}

	return fmt.Sprintf("%s  %s  %s  %s  %s",
		symbolStyle.Render(symbol),
		actionStyle.Render(fmt.Sprintf("%-*s", w.action, item.Action)),
		typeStyle.Render(fmt.Sprintf("%-*s", w.typ, item.Type)),
		nameStyle.Render(fmt.Sprintf("%-*s", w.name, item.Name)),
		detailStyle.Render(item.Detail),
	)
}

// planSymbol returns the diff symbol and its style for a given operation.
func planSymbol(op PlanOp) (string, lipgloss.Style) {
	switch op {
	case PlanCreate:
		return "+", lipgloss.NewStyle().Foreground(Palette.Success)
	case PlanModify:
		return "~", lipgloss.NewStyle().Foreground(Palette.Warning)
	case PlanDestroy:
		return "-", lipgloss.NewStyle().Foreground(Palette.Error)
	case PlanNoChange:
		return "=", lipgloss.NewStyle().Foreground(Palette.Muted)
	case PlanDetail:
		return "+", lipgloss.NewStyle().Foreground(Palette.Success)
	}
	return " ", lipgloss.NewStyle()
}
