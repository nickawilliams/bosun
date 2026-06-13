package ui

// The column grid. Every bosun component — cards, group children,
// plan rows, the lifecycle stepper, and huh forms — positions its
// glyphs and content on one shared grid so that "where does this go"
// has a single answer at any nesting depth.
//
// Three parameters generate the whole grid:
//
//	LeftPad    1  leading space before any level-0 glyph
//	IndentUnit 4  columns added per nesting level (" │  " spine + gap)
//	GlyphGap   2  spaces between a glyph and the content it heads
//
// From those, two column functions (1-indexed):
//
//	GlyphCol(L)   = 2 + 4L   → 2, 6, 10, 14 …   spine / state glyph
//	ContentCol(L) = 5 + 4L   → 5, 9, 13, 17 …   title / body / value
//
// A card at level L draws its spine at GlyphCol(L) and its title +
// body at ContentCol(L). Its Item rows, group children, and plan
// rows are level L+1: glyph at GlyphCol(L+1), content at
// ContentCol(L+1).
//
// Form fields obey the same grid (a form is a card; its fields are
// level-(L+1) rows):
//
//   - The focus cursor ">" pins to GlyphCol(L)+1 — flush right of the
//     spine, decoupled from the content grid, so it never competes
//     with content for a column.
//   - Single-value fields (text input, single select) place their
//     value at ContentCol(L), aligned with the field title.
//   - Multi-select rows carry per-item state glyphs, so they drop to
//     level L+1: glyph at GlyphCol(L+1), content at ContentCol(L+1).
//
// Trees are the one deliberate exception: their ├──/└── connectors
// own the horizontal "reach" to a node, so the glyph→label gap is 1
// space (not GlyphGap's 2). That tighter gap is a signal — it marks
// the boundary where the reader leaves the card stack for
// hierarchical data. See tree.go.
const (
	LeftPad    = 1
	IndentUnit = 4
	GlyphGap   = 2
)

// FocusMarker prefixes the focused field in a form — the cursor that
// pins to GlyphCol(L)+1, flush right of the spine. A single-width
// glyph plus a trailing space, so a single-value field's content
// lands at ContentCol(L). Shared by the theme's select selectors
// (SetString) and the cli's text-input constructors (.Prompt), since
// lipgloss can't inject the string into the input prompt via the
// theme without doubling it onto bubbles' default "> ".
const FocusMarker = "❭ "

// GlyphCol returns the 1-indexed column of a level-L glyph or spine.
func GlyphCol(level int) int {
	return LeftPad + 1 + level*IndentUnit
}

// ContentCol returns the 1-indexed column where level-L content
// (title, body, single-value field) begins.
func ContentCol(level int) int {
	return GlyphCol(level) + 1 + GlyphGap
}
