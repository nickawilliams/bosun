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
)

// PlanItem describes a single action in a plan.
type PlanItem struct {
	Op     PlanOp
	Action string // "Create Pull Request" or just "Pull Request" (for =)
	Target string // repo name, issue key, channel name
	Detail string // branch name, PR number, status transition
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
func (p *Plan) Add(op PlanOp, action, target, detail string) *Plan {
	p.items = append(p.items, PlanItem{Op: op, Action: action, Target: target, Detail: detail})
	return p
}

// IsEmpty returns true if the plan has no items.
func (p *Plan) IsEmpty() bool {
	return len(p.items) == 0
}

// HasChanges returns true if the plan has any non-NoChange items.
func (p *Plan) HasChanges() bool {
	for _, item := range p.items {
		if item.Op != PlanNoChange {
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

	// Compute column widths for alignment.
	maxAction := 0
	maxTarget := 0
	for _, item := range p.items {
		if len(item.Action) > maxAction {
			maxAction = len(item.Action)
		}
		if len(item.Target) > maxTarget {
			maxTarget = len(item.Target)
		}
	}

	// Render each item.
	for _, item := range p.items {
		symbol, symbolStyle := planSymbol(item.Op)
		actionStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
		targetStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		detailStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)

		// NoChange items are entirely muted.
		if item.Op == PlanNoChange {
			actionStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			targetStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			detailStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		}

		paddedAction := fmt.Sprintf("%-*s", maxAction, item.Action)
		paddedTarget := fmt.Sprintf("%-*s", maxTarget, item.Target)

		line := fmt.Sprintf(" %s    %s  %s  %s  %s",
			connStyle.Render("│"),
			symbolStyle.Render(symbol),
			actionStyle.Render(paddedAction),
			targetStyle.Render(paddedTarget),
			detailStyle.Render(item.Detail),
		)

		fmt.Fprintf(&b, "%s\n", line)
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

	maxAction := 0
	maxTarget := 0
	for _, item := range p.items {
		if len(item.Action) > maxAction {
			maxAction = len(item.Action)
		}
		if len(item.Target) > maxTarget {
			maxTarget = len(item.Target)
		}
	}

	var b strings.Builder
	for _, item := range p.items {
		symbol, symbolStyle := planSymbol(item.Op)
		actionStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
		targetStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		detailStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)

		if item.Op == PlanNoChange {
			actionStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			targetStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			detailStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		}

		paddedAction := fmt.Sprintf("%-*s", maxAction, item.Action)
		paddedTarget := fmt.Sprintf("%-*s", maxTarget, item.Target)

		fmt.Fprintf(&b, "  %s  %s  %s  %s\n",
			symbolStyle.Render(symbol),
			actionStyle.Render(paddedAction),
			targetStyle.Render(paddedTarget),
			detailStyle.Render(item.Detail),
		)
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

	if n := counts[PlanCreate]; n > 0 {
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

	if n := counts[PlanCreate]; n > 0 {
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
// by PlanCard. Each line includes the symbol, action, target, and detail
// but no spine or heading.
func (p *Plan) RenderItemLines() []string {
	if len(p.items) == 0 {
		return nil
	}

	maxAction := 0
	maxTarget := 0
	for _, item := range p.items {
		if len(item.Action) > maxAction {
			maxAction = len(item.Action)
		}
		if len(item.Target) > maxTarget {
			maxTarget = len(item.Target)
		}
	}

	var lines []string
	for _, item := range p.items {
		symbol, symbolStyle := planSymbol(item.Op)
		actionStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
		targetStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
		detailStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)

		if item.Op == PlanNoChange {
			actionStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			targetStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
			detailStyle = lipgloss.NewStyle().Foreground(Palette.Muted)
		}

		paddedAction := fmt.Sprintf("%-*s", maxAction, item.Action)
		paddedTarget := fmt.Sprintf("%-*s", maxTarget, item.Target)

		lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s",
			symbolStyle.Render(symbol),
			actionStyle.Render(paddedAction),
			targetStyle.Render(paddedTarget),
			detailStyle.Render(item.Detail),
		))
	}

	return lines
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
	}
	return " ", lipgloss.NewStyle()
}
