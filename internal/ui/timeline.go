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

// openCardRecord holds the card currently sitting at the bottom of
// the timeline, as BOTH already-rendered forms.
//
// Both are snapshots taken at print time, deliberately: the record
// outlives the print, and a card is a live pointer its owner may keep
// mutating (PlanCard walks Applying → Success in place). Re-rendering
// at rewrite time would paint the card's *later* state into the
// earlier state's rows, which is a duplicate at best and broken
// cursor math at worst. Snapshotting also pins the wrap widths, so a
// terminal resize between print and rewrite can't change the block's
// height out from under the cursor arithmetic.
//
// continuing has the same line count as open, structurally: both
// forms use a four-column gutter and the wrap widths don't depend on
// the form, so only the characters in the gutter differ.
type openCardRecord struct {
	open       string
	continuing string
}

// openCard is the record for the timeline's current tail, or nil when
// there is nothing to rewrite (nothing printed yet, the tail was
// erased by a rewind, the two forms are identical, or the timeline
// was closed). Single-goroutine access, same as needsSpacer.
var openCard *openCardRecord

// recordOpenCard marks a freshly printed block as the timeline's open
// card, given the exact bytes it painted in each form. Call it only
// when the cursor sits immediately below the block — the rewrite
// moves back over it and clears to the end of the screen, so anything
// printed underneath in between would be erased. Callers that hand
// the region off to something else (a huh form, a follow-up BubbleTea
// program) must not record.
//
// Nothing is recorded when there is nothing a rewrite would achieve:
// a zero-height block (an empty Plan), or a card whose two forms are
// identical — anything with no body lines, which is most cards, plus
// the root card's whole logo box. Skipping those is what keeps the
// mechanic invisible; repainting them would cost a flicker for no
// visible change.
//
// The record is returned so a caller that owns a transient block (a
// rewindable card) can hand it to discardRecord later.
func recordOpenCard(open, continuing string) *openCardRecord {
	if IsRaw() || !strings.Contains(open, "\n") || open == continuing {
		openCard = nil
		return nil
	}
	openCard = &openCardRecord{open: open, continuing: continuing}
	return openCard
}

// FinalizeOpenCard rewrites the timeline's open card into continuing
// form right now, then forgets it. Non-card output that breaks the
// cursor-position assumption — huh forms, raw blocks, anything that
// paints below the card without going through the spacer prefix —
// calls this first so the spine is restored before the region is
// handed off. Mirrors FlushSpacer's role for the connector row.
//
// A no-op when there is no open card, and in raw mode.
func FinalizeOpenCard() {
	if s := sessionActive(); s != nil {
		s.commitOpen()
		return
	}
	closeOpenCard()
}

// DiscardOpenCard forgets the open card WITHOUT rewriting it. Use
// when the block is about to be erased, or when something has already
// painted below it so the cursor math no longer holds — a rewrite
// there would clear live content.
func DiscardOpenCard() { openCard = nil }

// discardRecord forgets rec, but only if it is still the open card.
// Rewinds use this rather than DiscardOpenCard: if anything printed
// between the rewindable block and its rewind, the record now belongs
// to that successor, and dropping it would leave the successor
// permanently spineless.
func discardRecord(rec *openCardRecord) {
	if rec != nil && openCard == rec {
		openCard = nil
	}
}

// closeOpenCard performs the open → continuing swap: move the cursor
// back to the first row of the recorded block, clear from there to the
// end of the screen, and re-emit the block with its spine. Line counts
// match, so the cursor lands exactly where it started.
func closeOpenCard() {
	rec := openCard
	openCard = nil
	if rec == nil || IsRaw() {
		return
	}
	lines := strings.Count(rec.open, "\n")
	if !rewriteFits(lines, TermHeight()) {
		return
	}
	fmt.Printf("\x1b[%dF\x1b[J", lines)
	fmt.Print(rec.continuing)
}

// rewriteFits reports whether a block of the given height can be
// rewritten in place on a viewport of the given height.
//
// Cursor-previous-line clamps at row 1 of the SCREEN; it cannot walk
// back into scrollback. Once a block is as tall as the viewport its
// top row has scrolled off, so the move-up lands mid-block, the
// clear-to-end wipes only the visible remainder, and re-emitting the
// full block duplicates the rows above the cursor — a card printed
// twice, with the timeline shifted under it.
//
// Cards do get that tall: a failed subprocess's captured stdout, a
// deploy-targets card on a many-repo workspace, a long plan. Leaving
// one of those spineless is a missing connector; rewriting it is
// scrambled output. Skip.
func rewriteFits(blockLines, viewportRows int) bool {
	return blockLines > 0 && blockLines < viewportRows
}
