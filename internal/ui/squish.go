package ui

import (
	"fmt"
	"strings"
)

// Root-card breadcrumb absorption ("squishing").
//
// When a CardRoot is printed, the next non-root card emitted gets
// absorbed: its title and glyph are relocated to the root's
// breadcrumb trailing slot, and any body content is rendered below
// the root box. This lets lifecycle commands show async-fetched
// content (like an issue card) as root-adjacent output without a
// separate card in the timeline.
//
// The trailing slot is owned by the breadcrumb component; squish
// is the explicit glue that reads from the source card and writes
// into the destination breadcrumb via breadcrumb.SetTrailing().
//
// A card with no title and no body is inert — it consumes the
// squish slot without modifying the root. DismissSquish() is the
// explicit escape hatch for commands that don't want the next card
// absorbed (e.g., doctor, config, demo).

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

// DismissSquish drops any pending root-card absorption so the next
// card prints normally below the root instead of being absorbed
// into its breadcrumb trailing slot. Use after rootCard.Print()
// when the cards that follow are standalone content (e.g., demo
// output, groups) rather than squish targets.
func DismissSquish() { clearSquish() }
