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
// A card with no title and no body consumes the squish slot without
// modifying the root — emit an inert card to opt out of absorption
// for the following card.

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
	defer clearSquish()

	if !cardAbsorbs(c) {
		// Inert card — don't modify the root, don't render anything.
		return
	}

	root := squishCurrent.root
	lines := squishCurrent.lineCount

	// Erase the previously-rendered root card.
	if lines > 0 {
		fmt.Printf("\x1b[%dF\x1b[J", lines)
	}

	// Build extended title: "<root title> › <child title>". The
	// child's state glyph is omitted here — RunCard absorption
	// (see runcard.go) handles the spinner glyph specially with
	// state-aware coloring; static absorption just contributes
	// the title text.
	extended := *root
	if extended.title == "" {
		extended.title = c.title
	} else {
		extended.title = extended.title + " › " + c.title
	}

	// Re-render the extended root.
	rendered := extended.Render()
	fmt.Print(rendered)

	// Snapshot the new root so chained absorption (a second non-root
	// card after this one) can extend further still.
	snapshot := extended
	squishCurrent = squishState{
		root:      &snapshot,
		lineCount: strings.Count(rendered, "\n"),
	}
	squishPending = true

	// Render the child's body (subtitle + body kinds) below the
	// extended root — same layout used by Card.renderInner for the
	// non-title portion of any card.
	bodyOut := c.renderBodyAndSubtitle()
	if bodyOut != "" {
		fmt.Print(bodyOut)
		// Body content invalidates further chained absorption — the
		// next card prints normally.
		clearSquish()
		comfyBreak = true
		return
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
