package cli

import "github.com/nickawilliams/bosun/internal/ui"

// Dialog renders a confirmation prompt as a CardInput card with a
// title and optional descriptive body, followed by a yes/no form
// with caller-named buttons. The card rewinds when the user
// answers, leaving the timeline clean for whatever response card
// the caller prints next.
//
// Use Dialog whenever a yes/no confirmation needs more visual
// presence than a plain inline prompt — e.g., destructive
// operations, gating between phases, or anywhere a heading and
// descriptive context would help the user decide.
//
// Usage:
//
//	confirmed, err := NewDialog("Delete workspace?").
//	    Description("This removes every worktree and branch.").
//	    Affirmative("Delete").
//	    Negative("Cancel").
//	    Default(false).
//	    Show()
//
// In non-interactive mode (no TTY, CI), Show() returns the default
// value without rendering anything — callers don't need to gate on
// isInteractive() themselves.
type Dialog struct {
	title       string
	description string
	affirmative string
	negative    string
	defaultYes  bool
}

// NewDialog creates a Dialog with the given title and sensible
// defaults ("Yes" / "No" buttons, default selection = yes).
func NewDialog(title string) *Dialog {
	return &Dialog{
		title:       title,
		affirmative: "Yes",
		negative:    "No",
		defaultYes:  true,
	}
}

// Description sets an optional descriptive body shown beneath the
// title. Empty (the default) renders no body. A trailing blank
// muted line is added automatically when a description is set so
// the buttons have breathing room beneath the prose.
func (d *Dialog) Description(s string) *Dialog {
	d.description = s
	return d
}

// Affirmative sets the "yes" button label (default "Yes").
func (d *Dialog) Affirmative(s string) *Dialog {
	d.affirmative = s
	return d
}

// Negative sets the "no" button label (default "No").
func (d *Dialog) Negative(s string) *Dialog {
	d.negative = s
	return d
}

// Default sets the initial selection (default true). Also the
// value returned in non-interactive mode.
func (d *Dialog) Default(yes bool) *Dialog {
	d.defaultYes = yes
	return d
}

// Show renders the dialog, blocks for an answer, and returns the
// user's choice. In non-interactive mode, returns the default value
// without rendering. Returns ErrCancelled if the user aborts with
// ctrl+c.
//
// The card rewinds whenever the user gives a clean answer — Yes
// or No. The caller is expected to emit a result card next (Saved,
// Skip, Complete, etc.) that carries whatever post-answer context
// matters; the question itself isn't part of the persisted record.
// Matches the typeahead / slot pattern: the prompt clears once it's
// answered, and the result stands alone in its place.
//
// Ctrl+c keeps the question visible. The form was interrupted, not
// answered, so the cancellation card the caller (or HandleError)
// emits next reads as a response to the question that's still on
// screen. Dialog requests a spacer so that card lays out cleanly
// against huh's parked help row.
func (d *Dialog) Show() (bool, error) {
	if !isInteractive() {
		return d.defaultYes, nil
	}

	card := ui.NewCard(ui.CardInput, d.title).AccentBody()
	if d.description != "" {
		card = card.Muted(d.description, "")
	}
	card.Tight()
	rewind := card.PrintRewindable()

	confirmed := d.defaultYes
	formErr := runForm(
		newConfirm().
			Affirmative(d.affirmative).
			Negative(d.negative).
			Value(&confirmed),
	)

	if formErr != nil {
		// Ctrl+c or other form-level abort — leave the question
		// visible as context for the cancellation card the caller
		// emits next, and pad with a spacer so it lays out cleanly
		// against huh's parked help row.
		ui.RequestSpacer()
		return confirmed, formErr
	}

	rewind()
	return confirmed, nil
}
