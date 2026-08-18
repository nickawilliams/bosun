// Package ui — box-drawing chrome.
//
// These are the structural characters that draw the timeline spine,
// card boxes, tree connectors, and horizontal rules. Unlike the
// state glyphs on the palette (see state_grammar.go), they are
// deliberately NOT themeable, for two reasons:
//
//   - They carry no state semantics. A ├ means "the structure
//     continues below", not "this thing succeeded". There is nothing
//     for a glyph mode to say about them.
//   - They are load-bearing for layout. Rules are drawn with
//     strings.Repeat against the terminal width, and callers
//     subtract fixed chrome widths from the available columns
//     (breadcrumb.RenderRow's boxInner-3, card.go's TermWidth()-4,
//     status_render.go's stepperSlotWidth). Every character here
//     must stay exactly one cell wide; a substitute that wasn't
//     would silently skew every rule in the app.
//
// Consolidated here so the vocabulary is greppable in one place even
// though it's fixed.
package ui

// Box-drawing characters. Each is exactly one terminal cell wide.
const (
	BoxVertical   = "│"
	BoxHorizontal = "─"
	BoxCornerTL   = "╭" // root card anchor; becomes the timeline spine
	BoxCornerTR   = "╮"
	BoxCornerBR   = "╯"
	BoxCornerBL   = "╰"
	BoxTee        = "├" // structure continues below this branch
	BoxElbow      = "└" // last branch; structure ends here
)

// Tree connectors, composed from the base characters above. The
// three-cell reach (connector + two rules + gap) is what puts a tree
// node's label at the same column as a card's content — see
// layout.go for why trees are the one deliberate exception to the
// standard glyph-to-label gap.
//
// All four are exactly treeIndentWidth cells wide; the renderer
// stacks them to build each row's prefix, so they must stay aligned
// with each other and with treeIndentWidth.
const (
	treeBranch = BoxTee + BoxHorizontal + BoxHorizontal + " "   // ├──
	treeLast   = BoxElbow + BoxHorizontal + BoxHorizontal + " " // └──
	treeDown   = BoxVertical + "   "                            // │
	treeBlank  = "    "

	// Each nesting level adds this many visual columns of indentation.
	treeIndentWidth = 4
)
