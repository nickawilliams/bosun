package cli

import (
	"errors"
	"strings"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// maxSelectHeight is the maximum number of visible options in a select list.
const maxSelectHeight = 10

// typeaheadInputOptional shows a single-line text input with a
// placeholder that describes what submitting nothing does; the return
// is the RAW entry, empty when the user accepted the placeholder.
//
// Returning the entry rather than the resolved value is what lets a
// caller keep a per-item default: a shared prompt over N repositories
// can offer "each repo's own default branch" and, on an empty submit,
// leave every repo resolving for itself — where a resolved-value
// prompt would freeze one literal onto all of them.
func typeaheadInputOptional(title, placeholder string) (string, error) {
	input, field := newDefaultInput(placeholder)
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(input); err != nil {
		return "", err
	}
	slot.Clear()
	entry := strings.TrimSpace(field.Entry())
	if entry == "" {
		// Record the placeholder — it's the answer the user accepted,
		// even when it describes a rule ("per repo") rather than a value.
		ui.Selected(title, placeholder)
		return "", nil
	}
	ui.Selected(title, entry)
	return entry, nil
}

// typeaheadText shows a multi-line text editor with the current value
// pre-filled. Returns the edited value.
func typeaheadText(title, current string) (string, error) {
	value := current
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		rawText().
			Value(&value),
	); err != nil {
		return current, err
	}
	slot.Clear()
	// Multi-line values (PR bodies) record as a bounded excerpt — the
	// full text lands on the artifact being edited, not the timeline.
	ui.Selected(title, ui.Excerpt(value, recordExcerptLines))
	return value, nil
}

// multiSelectConfig captures optional per-call behavior for
// typeaheadMultiSelect, set via multiSelectOption.
type multiSelectConfig struct {
	excludeUser string // drop this user from the options entirely
	promoteUser string // float this user to the top, labeled "<user> (me)"
}

// multiSelectOption configures a typeaheadMultiSelect call.
type multiSelectOption func(*multiSelectConfig)

// excludeUser removes username from the option list (case-insensitive).
// Use for reviewers: GitHub forbids a PR author from reviewing their own
// PR, so the author should never appear as a selectable reviewer.
func excludeUser(username string) multiSelectOption {
	return func(c *multiSelectConfig) { c.excludeUser = username }
}

// promoteUser floats username to the top of the list and labels it
// "<user> (me)" so the current user can pick themselves quickly. The
// option's value stays the bare username so the host still receives a
// plain login.
func promoteUser(username string) multiSelectOption {
	return func(c *multiSelectConfig) { c.promoteUser = username }
}

// typeaheadMultiSelect fetches options with a spinner, then presents a
// filterable multi-select. Any values in current that match fetched options
// are pre-selected. Returns the selected values or an error if the fetch
// fails or the user aborts.
func typeaheadMultiSelect(title string, current []string, fetch func() ([]string, error), opts ...multiSelectOption) ([]string, error) {
	var cfg multiSelectConfig
	for _, o := range opts {
		o(&cfg)
	}

	slot := ui.NewSlot()

	var items []string
	if err := slot.Run("fetching "+title, func() error {
		var e error
		items, e = fetch()
		return e
	}); err != nil {
		return nil, err
	}

	if cfg.excludeUser != "" {
		items = withoutUser(items, cfg.excludeUser)
		current = withoutUser(current, cfg.excludeUser)
	}
	if cfg.promoteUser != "" {
		items = userToFront(items, cfg.promoteUser)
	}

	if len(items) == 0 {
		slot.Clear()
		ui.Skip("no " + title + " found")
		return nil, nil
	}

	preselect := make(map[string]bool, len(current))
	for _, v := range current {
		preselect[v] = true
	}

	options := make([]huh.Option[string], len(items))
	for i, item := range items {
		label := item
		if cfg.promoteUser != "" && strings.EqualFold(item, cfg.promoteUser) {
			label = item + " (me)"
		}
		opt := huh.NewOption(label, item)
		if preselect[item] {
			opt = opt.Selected(true)
		}
		options[i] = opt
	}

	selected := make([]string, len(current))
	copy(selected, current)
	height := min(len(items), maxSelectHeight)
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(options...).
			Height(height).
			Value(&selected),
	); err != nil {
		if errors.Is(err, ErrCancelled) {
			return nil, err
		}
		return nil, err
	}
	slot.Clear()

	ui.SelectedMulti(title, selected)
	return selected, nil
}

// withoutUser returns items with any case-insensitive match for user
// removed, preserving order.
func withoutUser(items []string, user string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if !strings.EqualFold(it, user) {
			out = append(out, it)
		}
	}
	return out
}

// userToFront returns items with user floated to the front, preserving the
// relative order of the rest. If user is already present (case-insensitive)
// its canonical casing from items is kept; otherwise user is prepended so
// the current user can always pick themselves.
func userToFront(items []string, user string) []string {
	front := user
	rest := make([]string, 0, len(items))
	for _, it := range items {
		if strings.EqualFold(it, user) {
			front = it
			continue
		}
		rest = append(rest, it)
	}
	return append([]string{front}, rest...)
}
