package cli

import (
	"errors"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// maxSelectHeight is the maximum number of visible options in a select list.
const maxSelectHeight = 10

// typeaheadInput shows a single-line text input with the current value as a
// placeholder. Pressing Enter with no input accepts the current value.
func typeaheadInput(title, current string) (string, error) {
	var value string
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewInput().
			Placeholder(current).
			Value(&value),
	); err != nil {
		return current, err
	}
	slot.Clear()
	if value == "" {
		value = current
	}
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
	if err := slot.Run("Fetching "+title, func() error {
		var e error
		items, e = fetch()
		return e
	}); err != nil {
		return "", err
	}

	if len(items) == 0 {
		slot.Clear()
		ui.Skip("No " + title + " found")
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

// typeaheadMultiSelect fetches options with a spinner, then presents a
// filterable multi-select. Any values in current that match fetched options
// are pre-selected. Returns the selected values or an error if the fetch
// fails or the user aborts.
func typeaheadMultiSelect(title string, current []string, fetch func() ([]string, error)) ([]string, error) {
	slot := ui.NewSlot()

	var items []string
	if err := slot.Run("Fetching "+title, func() error {
		var e error
		items, e = fetch()
		return e
	}); err != nil {
		return nil, err
	}

	if len(items) == 0 {
		slot.Clear()
		ui.Skip("No " + title + " found")
		return nil, nil
	}

	preselect := make(map[string]bool, len(current))
	for _, v := range current {
		preselect[v] = true
	}

	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		opt := huh.NewOption(item, item)
		if preselect[item] {
			opt = opt.Selected(true)
		}
		opts[i] = opt
	}

	selected := make([]string, len(current))
	copy(selected, current)
	height := min(len(items), maxSelectHeight)
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(opts...).
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
