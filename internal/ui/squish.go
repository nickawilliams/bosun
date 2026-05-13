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

	// Build extended root with the child's title appended as a new
	// data segment. The renderer assembles the visible breadcrumb
	// as <data segments> › <command-path tail>; data segments take
	// the absorbedTitleColor when set, and the most recently
	// absorbed segment carries the absorbedGlyph (if any).
	extended := *root
	extended.dataSegments = append(append([]string{}, root.dataSegments...), c.title)
	if g := c.glyph(); g != "" && !c.suppressAbsorbedGlyph {
		extended.absorbedGlyph = g
	} else if c.suppressAbsorbedGlyph {
		extended.absorbedGlyph = ""
	}
	if c.absorbedTitleColor != nil {
		extended.absorbedTitleColor = c.absorbedTitleColor
	}

	// Always compute the full rendered output — the chain-absorption
	// snapshot below records its line count, which must reflect the
	// full root regardless of whether we used the partial fast path
	// to print only the breadcrumb line.
	rendered := extended.Render()

	// Fast path: when the breadcrumb is the LAST line of the root,
	// erase only that line and rewrite it in place. The logo box
	// above stays untouched on screen — eliminates the visible
	// erase-and-repaint flash. Falls back to full erase + full
	// repaint when the invariant doesn't hold.
	partial := root.BreadcrumbLineCount() == 1 && lines > 0
	if partial {
		fmt.Print("\x1b[1F\x1b[2K\r")
		fmt.Print(extended.RenderBreadcrumbLine())
	} else if lines > 0 {
		fmt.Printf("\x1b[%dF\x1b[J", lines)
		fmt.Print(rendered)
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

	// Body-less child: chain only when the card explicitly opts in
	// (Card.ChainAbsorption). Default is single-segment absorption,
	// so accidentally body-less cards don't silently steal the next
	// card's slot.
	if c.chainAbsorption {
		snapshot := extended
		squishCurrent = squishState{
			root:      &snapshot,
			lineCount: strings.Count(rendered, "\n"),
		}
		squishPending = true
	} else {
		clearSquish()
	}

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
