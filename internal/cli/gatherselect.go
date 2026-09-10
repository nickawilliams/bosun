package cli

import (
	"errors"
	"fmt"
	"sort"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// This file holds gatherSelect, the preload-informed multi-select
// flow (#120's readiness-informed picker, carved out of the cleanup
// command). Three phases, one timeline position:
//
//  1. Preload — a fan-out under a group card: every item's spinner
//     listed up front, each resolving in place to the caller's
//     verbose row(s) as its probe finishes (ui.FanOut).
//  2. Select — the group finalizes INTO the picker's input header
//     (ui.RunGroupThen's successor, so no empty-frame flash), and a
//     multi-select mounts beneath it whose rows the preload
//     informed: what is preselected, what is gated, what each row
//     says.
//  3. Record — on submit, one card replaces both: the preload's
//     rows with the selection folded in. Selected rows keep their
//     glyph and colors; unselected rows recede fully (the Services
//     card's excluded-row treatment).
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

	// Brief is the compact annotation dimmed beside the name in the
	// picker. Keep it scannable — the row sits beside many siblings
	// and must not wrap. Empty renders the bare name.
	Brief string

	// Detail is the full annotation for the record card, where the
	// verbose form lives; empty falls back to Brief.
	Detail string

	// Glyph is the pre-styled record-row glyph for a selected row
	// (the picker shows huh's selection marker in that column
	// instead, and unselected record rows recede to the inactive
	// ○). Empty defaults to the success check.
	Glyph string

	// Preselected marks the row checked when the picker opens.
	Preselected bool

	// Gate, when non-empty, rejects a submission that selects this
	// row while the flow's gate is closed — the message renders as
	// the form's validation error. The row stays listed and
	// toggleable either way: seeing what is gated (and why) is part
	// of the selection surface.
	Gate string
}

// gatherSelect describes one flow. All callbacks are indexed by
// item; Work runs concurrently (bounded by ui.FanOut's pool),
// Resolve and Item run after the item's Work completed.
type gatherSelect struct {
	// Title is the preload group's title and the record card's.
	Title string

	// Header is the input header the group finalizes into and the
	// form mounts beneath.
	Header string

	// N is the item count.
	N int

	// Label names item i's preload spinner row.
	Label func(i int) string

	// Work is item i's probe. Runs on a worker goroutine.
	Work func(i int)

	// Resolve emits item i's verbose preload row(s) through the
	// group reporter, replacing its spinner in place.
	Resolve func(i int, r ui.Reporter)

	// Item derives item i's picker/record presentation. Called after
	// the whole preload completed.
	Item func(i int) gatherSelectItem

	// RecordState supplies the record card's state, evaluated at
	// record time (after the preload, so it can aggregate what the
	// probes found). Nil defaults to CardSuccess.
	RecordState func() ui.CardState

	// GateOpen opens gated rows for selection (the caller's --force
	// posture).
	GateOpen bool
}

// run executes the flow and returns the selected item indices in
// listing order. A cancelled picker returns the form's error with
// the picker rows left on screen as residue — context for the
// caller's cancellation card.
func (gs gatherSelect) run() ([]int, error) {
	rewindHeader := ui.RunGroupThen(gs.Title, func(grp ui.Reporter) {
		ui.FanOut(grp, gs.N, 0, gs.Label, gs.Work, gs.Resolve)
	}, func() *ui.Card { return ui.NewCard(ui.CardInput, gs.Header).Tight() })

	items := make([]gatherSelectItem, gs.N)
	opts := make([]huh.Option[int], gs.N)
	for i := range opts {
		items[i] = gs.Item(i)
		opts[i] = huh.NewOption(gatherSelectLabel(items[i]), i).
			Selected(items[i].Preselected)
	}

	var picked []int
	field := fittedMultiSelect(opts, &picked)
	if !gs.GateOpen {
		field = field.Validate(func(sel []int) error {
			for _, i := range sel {
				if items[i].Gate != "" {
					return errors.New(items[i].Gate)
				}
			}
			return nil
		})
	}

	// The header normally mounted as the group's successor; when it
	// couldn't (capture / fallback renders), mount our own so the
	// form still sits under an input header.
	var slot *ui.Slot
	if rewindHeader == nil {
		slot = ui.NewSlot()
		slot.Show(ui.NewCard(ui.CardInput, gs.Header).Tight())
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

// recordCard renders the flow's third phase: every row in picker
// order with the selection folded in. Selected rows keep their glyph
// and colors with the full detail; unselected rows recede fully —
// glyph, name, and detail all muted behind the inactive ○.
func (gs gatherSelect) recordCard(items []gatherSelectItem, picked []int) *ui.Card {
	nameStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)
	glyphOff := muted.Render(ui.Palette.Inactive)

	pickedSet := make(map[int]bool, len(picked))
	for _, i := range picked {
		pickedSet[i] = true
	}

	state := ui.CardSuccess
	if gs.RecordState != nil {
		state = gs.RecordState()
	}

	card := ui.NewCard(state, gs.Title).
		Value(fmt.Sprintf("%d of %d selected", len(picked), len(items)))
	for i, it := range items {
		detail := it.Detail
		if detail == "" {
			detail = it.Brief
		}
		if !pickedSet[i] {
			content := it.Name
			if detail != "" {
				content += " · " + detail
			}
			card.Item(glyphOff, muted.Render(content))
			continue
		}
		glyph := it.Glyph
		if glyph == "" {
			glyph = glyphOK
		}
		content := nameStyle.Render(it.Name)
		if detail != "" {
			content += muted.Render(" · " + detail)
		}
		card.Item(glyph, content)
	}
	return card
}
