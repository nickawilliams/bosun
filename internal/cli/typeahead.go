package cli

import (
	"errors"
	"strings"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// maxSelectHeight is the maximum number of visible options in a select list.
const maxSelectHeight = 10

// typeaheadInput shows a single-line text input with the current value as a
// placeholder. Pressing Enter with no input accepts the current value.
func typeaheadInput(title, current string) (string, error) {
	input, field := newDefaultInput(current)
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(input); err != nil {
		return current, err
	}
	slot.Clear()
	value := field.Resolved()
	ui.Selected(title, value)
	return value, nil
}

// typeaheadText shows a multi-line text editor with the current value
// pre-filled. Returns the edited value.
func typeaheadText(title, current string) (string, error) {
	value := current
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewText().
			Value(&value),
	); err != nil {
		return current, err
	}
	slot.Clear()
	ui.Selected(title, value)
	return value, nil
}

// typeaheadSelect fetches options with a spinner, then presents a filterable
// single-select. If current is non-empty and matches an option, the selector
// starts with it highlighted. Returns the selected value or an error if the
// fetch fails or the user aborts.
func typeaheadSelect(title, current string, fetch func() ([]string, error)) (string, error) {
	slot := ui.NewSlot()

	var items []string
	if err := slot.Run("fetching "+title, func() error {
		var e error
		items, e = fetch()
		return e
	}); err != nil {
		return "", err
	}

	if len(items) == 0 {
		slot.Clear()
		ui.Skip("no " + title + " found")
		return "", nil
	}

	// Move the current value to the front so it's visible and pre-selected.
	if current != "" {
		for i, item := range items {
			if item == current && i > 0 {
				reordered := make([]string, 0, len(items))
				reordered = append(reordered, current)
				reordered = append(reordered, items[:i]...)
				reordered = append(reordered, items[i+1:]...)
				items = reordered
				break
			}
		}
	}

	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		opts[i] = huh.NewOption(item, item)
	}

	selected := current
	height := min(len(items), maxSelectHeight)
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Height(height).
			Value(&selected),
	); err != nil {
		if errors.Is(err, ErrCancelled) {
			return "", err
		}
		return "", err
	}
	slot.Clear()

	ui.Selected(title, selected)
	return selected, nil
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
