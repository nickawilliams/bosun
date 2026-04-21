package cli

import (
	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// typeaheadSelect fetches options with a spinner, then presents a filterable
// single-select. Returns the selected value or an error if the fetch fails or
// the user aborts.
func typeaheadSelect(title string, fetch func() ([]string, error)) (string, error) {
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

	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		opts[i] = huh.NewOption(item, item)
	}

	var selected string
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewSelect[string]().
			Options(opts...).
			Filtering(true).
			Value(&selected),
	); err != nil {
		return "", err
	}
	slot.Clear()

	return selected, nil
}

// typeaheadMultiSelect fetches options with a spinner, then presents a
// filterable multi-select. Returns the selected values or an error if the
// fetch fails or the user aborts.
func typeaheadMultiSelect(title string, fetch func() ([]string, error)) ([]string, error) {
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

	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		opts[i] = huh.NewOption(item, item)
	}

	var selected []string
	slot.Show(ui.NewCard(ui.CardInput, title).Tight())
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(opts...).
			Filtering(true).
			Value(&selected),
	); err != nil {
		return nil, err
	}
	slot.Clear()

	return selected, nil
}
