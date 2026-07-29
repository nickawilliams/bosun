package ui

import (
	"fmt"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

// DetailRef is a render-safe, apply-writable detail slot. Apply
// closures run on a worker goroutine while the plan card's render
// loop reads the item every frame, so the value goes through an
// atomic rather than a bare string the two goroutines would race on.
type DetailRef struct{ v atomic.Value }

// Set stores the resolved detail. Safe from any goroutine.
func (r *DetailRef) Set(s string) { r.v.Store(s) }

// Get returns the resolved detail, or "" while unresolved.
func (r *DetailRef) Get() string {
	s, _ := r.v.Load().(string)
	return s
}

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

	// DetailRef, when set and non-empty, overrides Detail at render
	// time. It lets an Apply closure resolve a value that's only known
	// after the action runs (a created issue key, a returned URL) —
	// the plan card's final success frame renders after apply, so the
	// resolved text lands in the same row that previously showed a
	// "known after apply" placeholder. Sibling of Action.OpRef, which
	// does the same for the operation glyph at assess time.
	DetailRef *DetailRef
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

// AddWithDetailRef appends a plan item whose detail can be superseded
// after apply: while ref is unset the static detail renders (typically
// carrying a "known after apply" placeholder); once an Apply closure
// calls ref.Set, subsequent renders — including the card's final
// success frame — show the resolved text instead.
func (p *Plan) AddWithDetailRef(op PlanOp, action, subjectType, name, detail string, ref *DetailRef) *Plan {
	p.items = append(p.items, PlanItem{Op: op, Action: action, Type: subjectType, Name: name, Detail: detail, DetailRef: ref})
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

// Render returns the plan as a styled string for display in the
// timeline. Builds a Card internally so plan rows render at the
// same depth as Card.Item rows in other cards (status, etc.).
func (p *Plan) Render() string {
	if len(p.items) == 0 {
		return ""
	}
	card := NewCard(CardInfo, "Plan").Value(p.Summary())
	p.AppendItemsToCard(card)
	return card.Render()
}

// AppendItemsToCard adds one Card.Item row per plan item to the
// given card and returns the card for chaining. Lets callers
// compose a card (with their choice of title/state/etc.) and then
// drop the plan's items into it without reaching into the plan's
// private column-width or item-formatting helpers.
func (p *Plan) AppendItemsToCard(c *Card) *Card {
	widths := p.columnWidths()
	for _, item := range p.items {
		glyph, content := planItemParts(item, widths)
		c.Item(glyph, content)
	}
	return c
}

// Print writes the plan to stdout.
func (p *Plan) Print() {
	fmt.Print(spacerPrefix() + p.Render())
}

// PrintRewindable writes the plan to stdout and returns a function that
// erases it (same pattern as Card.PrintRewindable).
func (p *Plan) PrintRewindable() func() {
	prev := needsSpacer
	rendered := spacerPrefix() + p.Render()
	fmt.Print(rendered)
	lines := strings.Count(rendered, "\n")
	return func() {
		if lines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", lines)
		}
		needsSpacer = prev
	}
}

// RenderItems returns the formatted action lines as a single string
// (newline-joined, no trailing newline) without heading or timeline
// spine. Each line is " <glyph>  <content>" with a leading space so
// glyphs land at the same column as Card.Item rows when the form's
// own border/padding supplies the column-2 spine. Suitable for
// embedding as Title content in a huh form, or anywhere else that
// wants raw rows.
func (p *Plan) RenderItems() string {
	if len(p.items) == 0 {
		return ""
	}

	widths := p.columnWidths()
	var b strings.Builder
	for _, item := range p.items {
		fmt.Fprintf(&b, " %s\n", renderPlanRow(item, widths))
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

// RenderItemLines returns the formatted action lines as a slice
// for reuse by PlanCard. Each line is "<glyph>  <content>" with
// no spine or heading.
func (p *Plan) RenderItemLines() []string {
	if len(p.items) == 0 {
		return nil
	}

	widths := p.columnWidths()
	var lines []string
	for _, item := range p.items {
		lines = append(lines, renderPlanRow(item, widths))
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

// planItemParts splits a PlanItem into its styled glyph (the diff
// symbol) and the column-aligned content string. Both pieces are
// pre-rendered with their semantic colors. The glyph is used as
// Card.Item's first argument; content as the second. For embedded
// uses (RenderItems / RenderItemLines / PlanCard body), callers
// can rejoin via "<glyph>  <content>".
func planItemParts(item PlanItem, w planColumnWidths) (glyph, content string) {
	symbol, symbolStyle := planSymbol(item.Op)
	g := symbolStyle.Render(symbol)

	actionStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
	typeStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	nameStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	detailStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
	if item.Op == PlanNoChange {
		muted := lipgloss.NewStyle().Foreground(Palette.Muted)
		actionStyle, typeStyle, nameStyle, detailStyle = muted, muted, muted, muted
	}

	detail := item.Detail
	if item.DetailRef != nil {
		if d := item.DetailRef.Get(); d != "" {
			detail = d
		}
	}
	c := fmt.Sprintf("%s  %s  %s  %s",
		actionStyle.Render(fmt.Sprintf("%-*s", w.action, item.Action)),
		typeStyle.Render(fmt.Sprintf("%-*s", w.typ, item.Type)),
		nameStyle.Render(fmt.Sprintf("%-*s", w.name, item.Name)),
		detailStyle.Render(detail),
	)
	return g, c
}

// renderPlanRow returns "<glyph>  <content>" for a single plan
// item. Used by RenderItems / RenderItemLines / PlanCard for
// non-Card-body embedding. Card.Item-based rendering uses
// planItemParts directly.
func renderPlanRow(item PlanItem, w planColumnWidths) string {
	glyph, content := planItemParts(item, w)
	return glyph + "  " + content
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
