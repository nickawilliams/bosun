package ui

import (
	"fmt"
	"strings"
)

// Package ui — timeline termination.
//
// A run of cards reads as one vertical timeline because every card
// draws the spine (│) down its body lines. That works everywhere
// except at the bottom: after the last card prints, the spine stops
// mid-stream and the timeline looks unterminated, as if more content
// was meant to follow.
//
// The fix is to render each card in one of two forms and swap between
// them as the timeline grows:
//
//	open        the card is the last thing in the timeline — body
//	            lines carry no spine, so the run visibly ends
//	continuing  a successor exists below — body lines carry the spine
//
// A card always prints in open form. When the next card prints, the
// previous one is rewritten in place (cursor-up + clear-to-end, the
// same mechanic the spinner finalizers use) into continuing form. The
// most recent card is therefore always open, and EndTimeline
// deliberately does NOT rewrite — leaving the last card open is
// exactly what gives the visual "this is the end."
type timelineForm int

const (
	// formOpen renders a card as the tail of the timeline.
	formOpen timelineForm = iota
	// formContinuing renders a card that has a successor beneath it.
	formContinuing
)

// openCardRecord remembers enough about the card currently sitting at
// the bottom of the timeline to rewrite it in continuing form: the
// bytes it painted (so the cursor can be moved back over exactly that
// block, and so a rewrite that would change nothing can be skipped)
// and how to re-render it with the spine.
//
// continuing must produce the same line count as the open render did.
// It does, structurally: both forms use a four-column gutter and the
// wrap widths don't depend on the form, so only the characters in the
// gutter differ.
type openCardRecord struct {
	open       string
	continuing func() string
}

// openCard is the record for the timeline's current tail, or nil when
// there is nothing to rewrite (nothing printed yet, the tail was
// erased by a rewind, or the timeline was closed). Single-goroutine
// access, same as needsSpacer.
var openCard *openCardRecord

// recordOpenCard marks a freshly printed block as the timeline's open
// card, given the exact bytes it painted. Call it only when the cursor
// sits immediately below the block — the rewrite moves back over it
// and clears to the end of the screen, so anything printed underneath
// in between would be erased. Callers that hand the region off to
// something else (a huh form, a follow-up BubbleTea program) must not
// record.
//
// A zero-line block records nothing: there is no block to rewrite, and
// the previous record was already consumed by the spacer prefix.
func recordOpenCard(open string, continuing func() string) {
	if IsRaw() || !strings.Contains(open, "\n") {
		openCard = nil
		return
	}
	openCard = &openCardRecord{open: open, continuing: continuing}
}

// FinalizeOpenCard rewrites the timeline's open card into continuing
// form right now, then forgets it. Non-card output that breaks the
// cursor-position assumption — huh forms, raw blocks, anything that
// paints below the card without going through the spacer prefix —
// calls this first so the spine is restored before the region is
// handed off. Mirrors FlushSpacer's role for the connector row.
//
// A no-op when there is no open card, and in raw mode.
func FinalizeOpenCard() { closeOpenCard() }

// DiscardOpenCard forgets the open card WITHOUT rewriting it. Use
// when the block is about to be erased, or when something has already
// painted below it so the cursor math no longer holds — a rewrite
// there would clear live content.
func DiscardOpenCard() { openCard = nil }

// closeOpenCard performs the open → continuing swap: move the cursor
// back to the first row of the recorded block, clear from there to the
// end of the screen, and re-emit the block with its spine. Line counts
// match, so the cursor lands exactly where it started.
//
// A card whose two forms render identically — anything with no body
// lines, which is most of them, plus the root card's logo box — is
// left alone. Repainting those would cost a visible flicker for no
// visible change.
func closeOpenCard() {
	rec := openCard
	openCard = nil
	if rec == nil || IsRaw() {
		return
	}
	continuing := rec.continuing()
	if continuing == rec.open {
		return
	}
	fmt.Printf("\x1b[%dF\x1b[J", strings.Count(rec.open, "\n"))
	fmt.Print(continuing)
}
