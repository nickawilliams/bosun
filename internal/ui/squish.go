package ui

import (
	"fmt"
	"strings"
)

// Root-card breadcrumb absorption ("squishing").
//
// When a CardRoot is printed, the next non-root card emitted
// gets absorbed: instead of printing as a separate timeline card,
// its title is appended to the root's breadcrumb (separated by
// " › ") and any body content is rendered below the re-rendered
// root box. This lets call sites compose normally — the first
// "what we're doing" card always lives in the breadcrumb without
// requiring a special API.
//
// By default, absorption ends after a single segment — a body-less
// absorbed card stops the chain so the next card prints normally.
// Multi-segment breadcrumbs (e.g., `bosun › Foo › Bar › Baz`) are
// opt-in via Card.ChainAbsorption(): a body-less absorbed card
// marked with that method keeps the squish chain armed so the
// next non-root card absorbs as another segment.
//
// A card with no title and no body is inert — it consumes the
// squish slot without modifying the root (use to opt out of
// absorption for the following card when ChainAbsorption was set).

// squishState captures the most recent root-card print so the next
// non-root print can re-render it with an extended breadcrumb.
type squishState struct {
	root      *Card // copy of the last-printed root card
	lineCount int   // line count of its rendered output
}

var (
	squishPending bool
	squishCurrent squishState
)

// rememberRootForSquish records the root card just printed so the
// next non-root card can absorb its title into the breadcrumb. For
// non-root cards, clears any pending squish (unrelated content
// invalidates the slot).
func rememberRootForSquish(c *Card, rendered string) {
	if c.state == CardRoot {
		// Take a shallow copy; the absorbed render mutates only the
		// title field, leaving the original card untouched.
		snapshot := *c
		squishCurrent = squishState{
			root:      &snapshot,
			lineCount: strings.Count(rendered, "\n"),
		}
		squishPending = true
		return
	}
	clearSquish()
}

// squishConsume handles a non-root card emitted while a squish is
// pending. Cards with content extend the breadcrumb; inert cards
// consume the slot as a noop.
func squishConsume(c *Card) {
	if !cardAbsorbs(c) {
		// Inert card — don't modify the root, don't render anything.
		// Drop the squish slot so the next card prints normally.
		clearSquish()
		return
	}

	root := squishCurrent.root
	lines := squishCurrent.lineCount

	// Build extended root: the child's title and glyph land in the
	// breadcrumb's trailing slot. The breadcrumb component owns its
	// rendering; squish is the explicit glue that reads from the
	// source card and writes into the destination breadcrumb's slot.
	extended := *root
	extended.breadcrumb = root.breadcrumb.copy()
	extended.breadcrumb.SetTrailing(c.title, c.glyph(), c.suppressAbsorbedGlyph)

	// Fast path: when the breadcrumb is the LAST line of the root,
	// rewrite it in place WITHOUT clearing first — the new content
	// (always wider, since it gained a segment) covers the old line
	// cell-by-cell. The breadcrumb width is fixed (rule chars fill to
	// box-width), so old chars can't leak past the new content. Logo
	// box above stays untouched on screen.
	//
	// Falls back to full erase + full repaint when the invariant
	// doesn't hold (e.g., a future multi-line breadcrumb).
	partial := root.BreadcrumbLineCount() == 1 && lines > 0
	if partial {
		fmt.Print("\x1b[1F\r")
		fmt.Print(extended.RenderBreadcrumbLine())
	} else if lines > 0 {
		fmt.Printf("\x1b[%dF\x1b[J", lines)
		fmt.Print(extended.Render())
	}

	// Render the child's body (subtitle + body kinds) below the
	// extended root — same layout used by Card.renderInner for the
	// non-title portion of any card.
	bodyOut := c.renderBodyAndSubtitle()
	if bodyOut != "" {
		fmt.Print(bodyOut)
		// Body content closes the chain — the next card prints
		// normally below the extended root.
		clearSquish()
		comfyBreak = true
		return
	}

	// Body-less child: drop the squish slot. (Chain-absorption was
	// removed in the breadcrumb separation refactor — the trailing
	// slot is the canonical home for an absorbed card's identity,
	// so chained-segment absorption no longer applies.)
	clearSquish()

	if !c.tight {
		comfyBreak = true
	}
}

// cardAbsorbs reports whether a non-root card has any content
// worth squishing. Empty title + no body + no subtitle → inert.
func cardAbsorbs(c *Card) bool {
	return c.title != "" || len(c.body) > 0 || c.subtitle != ""
}

// clearSquish drops any pending absorption state.
func clearSquish() {
	squishPending = false
	squishCurrent = squishState{}
}
