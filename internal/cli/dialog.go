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

// Show renders the dialog, blocks for an answer, rewinds the card,
// and returns the user's choice. In non-interactive mode, returns
// the default value without rendering. Returns ErrCancelled if the
// user aborts with ctrl+c.
func (d *Dialog) Show() (bool, error) {
	if !isInteractive() {
		return d.defaultYes, nil
	}

	card := ui.NewCard(ui.CardInput, d.title)
	if d.description != "" {
		card = card.Muted(d.description, "")
	}
	rewind := card.Tight().PrintRewindable()

	confirmed := d.defaultYes
	err := runForm(
		newConfirm().
			Affirmative(d.affirmative).
			Negative(d.negative).
			Value(&confirmed),
	)
	rewind()
	if err != nil {
		return d.defaultYes, err
	}
	return confirmed, nil
}
