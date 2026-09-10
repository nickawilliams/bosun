package cli

import (
	"fmt"
	"sort"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// This file holds gatherSelect, the preload-informed multi-select
// flow (#120's readiness-informed picker, carved out of the cleanup
// command). One title, one timeline position, four phases:
//
//  1. Discover (optional) — resolve the item set under the flow's
//     header, captioned by StatusResolving. Zero items (or an
//     error) ends the flow with the group card standing.
//  2. Preload — a fan-out under the same card, captioned by
//     StatusChecking: every item's spinner listed up front, each
//     resolving in place to the caller's row(s) as its probe
//     finishes (ui.FanOut).
//  3. Select — the group finalizes INTO the picker's input header
//     (ui.RunGroupThen's successor, so no empty-frame flash),
//     captioned by StatusPicker, and a multi-select mounts beneath
//     it whose rows the preload informed: what arrives preselected
//     and what each row says. Every row is selectable — checking
//     one that didn't arrive preselected is the user's override,
//     never a gated action.
//  4. Record — on submit, one card replaces everything: the rows
//     with the selection folded in. Selected rows keep their glyph
//     and colors; unselected rows recede fully (the Services card's
//     excluded-row treatment).
//
// It lives in cli rather than ui because the form plumbing does
// (runForm, fittedMultiSelect, the huh theme) — the same layering
// as runPlanCard. Interactive-only: non-interactive callers own
// their static-card path, where the set was already selected by
// flags and there is nothing to pick.

// gatherSelectItem is one row of the flow, derived by the caller
// after its preload probe resolved.
type gatherSelectItem struct {
	// Name is the row's identifier: bold in the picker, primary-
	// colored in the record.
	Name string

	// Brief is the compact annotation beside the name — dimmed in
	// the picker, muted in the record. The flow speaks one
	// vocabulary across all phases, so keep it scannable: the row
	// sits beside many siblings and must not wrap. Verbose forms
	// belong to surfaces outside the flow (cleanup's raw readiness
	// card, say). Empty renders the bare name.
	Brief string

	// Preselected marks the row checked when the picker opens —
	// the safe-by-default set a bare Enter accepts. Every row is
	// selectable regardless: checking a row that didn't arrive
	// preselected IS the user's acknowledgment of whatever its
	// annotation warns about, and the caller treats that selection
	// as the override.
	Preselected bool
}

// gatherSelect describes one flow. All callbacks are indexed by
// item; Work runs concurrently (bounded by ui.FanOut's pool),
// Resolve and Item run after the item's Work completed.
type gatherSelect struct {
	// Title names every phase: the live group card, the picker's
	// input header, and the record card.
	Title string

	// Discover, when set, resolves the item set as the flow's first
	// phase and returns the item count (N is ignored then). Zero
	// items — or an error, which run returns — ends the flow before
	// the picker: the group card stands (captioned by StatusEmpty)
	// and run returns a nil selection. The closure typically stores
	// its own detail (targets, skip diagnostics) for the caller to
	// use and report after run returns.
	Discover func() (int, error)

	// N is the item count when Discover is nil.
	N int

	// StatusResolving captions the discover phase (e.g. "Resolving
	// workspaces..."). Empty leaves the title bare.
	StatusResolving string

	// StatusChecking captions the preload phase, evaluated once the
	// item count is known (e.g. "25 workspaces found, checking
	// readiness...").
	StatusChecking func(n int) string

	// StatusEmpty captions the standing group card when Discover
	// finds nothing — a resolved wording replacing the in-progress
	// StatusResolving caption.
	StatusEmpty string

	// StatusPicker captions the picker header and is evaluated after
	// the preload, with the item count and how many arrived
	// preselected (e.g. "25 workspaces found, 16 workspaces ready").
	StatusPicker func(n, preselected int) string

	// Label names item i's preload spinner row.
	Label func(i int) string

	// Work is item i's probe. Runs on a worker goroutine.
	Work func(i int)

	// Resolve emits item i's row(s) through the group reporter,
	// replacing its spinner in place.
	Resolve func(i int, r ui.Reporter)

	// Item derives item i's picker/record presentation. Called after
	// the whole preload completed.
	Item func(i int) gatherSelectItem
}

// run executes the flow and returns the selected item indices in
// listing order. A discover phase that found nothing returns
// (nil, nil) — the caller reports why from its own discover detail.
// A cancelled picker returns the form's error with the picker rows
// left on screen as residue — context for the caller's cancellation
// card.
func (gs gatherSelect) run() ([]int, error) {
	n := gs.N
	var discoverErr error
	var items []gatherSelectItem
	preselected := 0

	rewindHeader := ui.RunGroupThen(gs.Title, func(grp ui.Reporter) {
		status := func(text string) {
			if text == "" {
				return
			}
			if sr, ok := grp.(ui.StatusReporter); ok {
				sr.Status(text)
			}
		}
		if gs.Discover != nil {
			status(gs.StatusResolving)
			n, discoverErr = gs.Discover()
		}
		if discoverErr != nil {
			// The returned error is the authority on what went
			// wrong; the card keeps its last caption.
			return
		}
		if n == 0 {
			status(gs.StatusEmpty)
			return
		}
		if gs.StatusChecking != nil {
			status(gs.StatusChecking(n))
		}
		ui.FanOut(grp, n, 0, gs.Label, gs.Work, gs.Resolve)
		items = make([]gatherSelectItem, n)
		for i := range items {
			items[i] = gs.Item(i)
			if items[i].Preselected {
				preselected++
			}
		}
	}, func() *ui.Card {
		if items == nil {
			return nil // nothing to pick — the group card stands
		}
		return gs.pickerHeader(n, preselected)
	})
	if discoverErr != nil {
		return nil, discoverErr
	}
	if items == nil {
		return nil, nil
	}

	opts := make([]huh.Option[int], n)
	for i := range opts {
		opts[i] = huh.NewOption(gatherSelectLabel(items[i]), i).
			Selected(items[i].Preselected)
	}

	var picked []int
	field := fittedMultiSelect(opts, &picked)

	// The header normally mounted as the group's successor; when it
	// couldn't (capture / fallback renders), mount our own so the
	// form still sits under an input header.
	var slot *ui.Slot
	if rewindHeader == nil {
		slot = ui.NewSlot()
		slot.Show(gs.pickerHeader(n, preselected))
	}
	if err := runForm(field); err != nil {
		ui.RequestSpacer()
		return nil, err
	}
	if slot != nil {
		slot.Clear()
	} else {
		rewindHeader()
	}

	sort.Ints(picked)
	gs.recordCard(items, picked).Print()
	return picked, nil
}

// pickerHeader builds the input header the form mounts beneath —
// the successor the group finalizes into, and the fallback header
// for renders without one. One constructor so the two can't drift.
func (gs gatherSelect) pickerHeader(n, preselected int) *ui.Card {
	header := ui.NewCard(ui.CardInput, gs.Title)
	if gs.StatusPicker != nil {
		header.Muted(gs.StatusPicker(n, preselected))
	}
	return header
}

// gatherSelectLabel renders one picker row: the item name in bold
// and its brief annotation dimmed after it.
//
// The emphasis uses raw SGR intensity toggles (bold on/off 1/22,
// dim on/off 2/22), NOT lipgloss — the emitDeploymentSources
// precedent: a lipgloss render closes with a full SGR reset that
// wipes huh's own selection/focus styling for the rest of the line,
// while attribute toggles compose with whatever foreground huh's
// option styles apply. Foreground color stays off-limits here for
// the same reason — there is no "restore huh's color" code, only
// reset-to-default.
func gatherSelectLabel(it gatherSelectItem) string {
	name := "\x1b[1m" + it.Name + "\x1b[22m"
	if it.Brief == "" {
		return name
	}
	return fmt.Sprintf("%s\x1b[2m · %s\x1b[22m", name, it.Brief)
}

// recordCard renders the flow's final phase: every row in picker
// order with the selection folded in, beneath the selection tally.
// The whole card speaks selection, not any per-item vocabulary —
// the card state is the success of the completed selection, and the
// glyph column freezes the picker's final state (✓ selected, the
// inactive ○ unselected) so the visual history of what the user
// chose survives in scrollback; the brief annotation still says why
// an unselected row sat unchecked. Unselected rows recede fully —
// glyph, name, and annotation all muted.
func (gs gatherSelect) recordCard(items []gatherSelectItem, picked []int) *ui.Card {
	nameStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOn := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)
	glyphOff := muted.Render(ui.Palette.Inactive)

	pickedSet := make(map[int]bool, len(picked))
	for _, i := range picked {
		pickedSet[i] = true
	}

	card := ui.NewCard(ui.CardSuccess, gs.Title).
		Muted(fmt.Sprintf("%d of %d selected", len(picked), len(items)), "")
	for i, it := range items {
		if !pickedSet[i] {
			content := it.Name
			if it.Brief != "" {
				content += " · " + it.Brief
			}
			card.Item(glyphOff, muted.Render(content))
			continue
		}
		content := nameStyle.Render(it.Name)
		if it.Brief != "" {
			content += muted.Render(" · " + it.Brief)
		}
		card.Item(glyphOn, content)
	}
	return card
}
