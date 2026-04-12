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

	fmt.Fprintf(&b, " %s  %s\n", connStyle.Render("│"), headingStyle.Render("Plan:"))

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

	// Summary line.
	fmt.Fprintf(&b, " %s\n", connStyle.Render("│"))
	summary := p.summary()
	if summary != "" {
		fmt.Fprintf(&b, " %s  %s\n", connStyle.Render("│"), lipgloss.NewStyle().Foreground(Palette.Muted).Render(summary))
	}

	return b.String()
}

// Print writes the plan to stdout.
func (p *Plan) Print() {
	fmt.Print(p.Render())
}

// summary builds the count line: "1 unchanged, 2 to create, 1 to update"
func (p *Plan) summary() string {
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
