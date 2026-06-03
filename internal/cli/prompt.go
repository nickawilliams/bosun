package cli

import (
	"errors"
	"fmt"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// formTheme is the shared huh theme derived from the app palette.
var formTheme = ui.FormTheme()

// isInteractive returns true when the current input stream supports
// prompting (real TTY stdin or an injected reader in tests).
func isInteractive() bool {
	return ui.Interactive()
}

// forceInteractive returns true if the user passed --interactive and
// the input stream is interactive. Use this to gate prompts for
// optional fields that would otherwise be silently skipped.
func forceInteractive(cmd *cobra.Command) bool {
	fi, _ := cmd.Flags().GetBool("interactive")
	return fi && isInteractive()
}

// runForm runs a huh form with the app theme applied. Keybinding
// help is explicitly enabled so the shortcut hints render in the
// footer beneath the active prompt. The timeline layout wraps
// huh's default layout to recolor field separator bars in the
// recessed timeline gray.
//
// Input/output flow through ui.Input() / ui.Output() so tests that
// inject streams via cmd.SetIn/SetOut drive prompts against buffers.
//
// If the user aborts with ctrl+c, returns ErrCancelled.
func runForm(fields ...huh.Field) error {
	// Refuse when either stdin can't be read interactively (CI /
	// piped stdin) or stdout can't render the prompt visibly (piped
	// stdout). Without the second check we'd block on TTY stdin while
	// the form's ANSI silently went into the pipe.
	if !ui.Interactive() || !ui.CanRenderInteractively() {
		return fmt.Errorf("interactive input required but stdin or stdout is not a terminal")
	}
	// Multi-field forms get a leading visual break between the
	// containing card (any prompt title/description above) and the
	// first field, regardless of whether the prior caller armed
	// needsSpacer (Tight cards intentionally clear it). Without this,
	// the form's first field title sits flush against the heading
	// above with no separation. Single-field forms keep the current
	// behavior — only emit a spacer if one is pending.
	if len(fields) > 1 {
		ui.ClearSpacer()
		fmt.Fprintln(ui.Output(), " "+lipgloss.NewStyle().Foreground(ui.Palette.Recessed).Render("│"))
		ui.RequestSpacer()
	} else {
		ui.FlushSpacer()
	}
	// WithWidth is critical when the output isn't a real terminal —
	// huh's bubbletea program never receives a resize message in that
	// case and otherwise crashes inside textinput on a 0-width slice.
	err := huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(formTheme).
		WithLayout(ui.NewTimelineLayout()).
		WithShowHelp(true).
		WithInput(ui.Input()).
		WithOutput(ui.Output()).
		WithWidth(ui.TermWidth()).
		Run()
	if errors.Is(err, huh.ErrUserAborted) {
		// BubbleTea leaves a trailing blank line on exit; move the
		// cursor up so the cancel card (or its comfy connector)
		// overwrites it rather than creating double spacing.
		_, _ = fmt.Fprint(ui.Output(), "\x1b[A")
		return ErrCancelled
	}
	return err
}

// newConfirm creates a huh Confirm field with app defaults (left-aligned buttons).
func newConfirm() *huh.Confirm {
	return huh.NewConfirm().WithButtonAlignment(lipgloss.Left)
}

// promptRequired prompts for a value if stdin is a terminal. If stdin is not
// a terminal (piped/scripted), returns empty string and the caller should
// error.
func promptRequired(label string) string {
	if !isInteractive() {
		return ""
	}

	var value string
	if err := runForm(huh.NewInput().Title(label).Value(&value)); err != nil {
		return ""
	}
	return value
}

// promptConfirm shows a yes/no confirmation. Returns true if confirmed.
// Defaults to the provided value. Returns (defaultVal, nil) in
// non-interactive mode. Returns ErrCancelled if the user presses ctrl+c.
func promptConfirm(label string, defaultVal bool) (bool, error) {
	if !isInteractive() {
		return defaultVal, nil
	}

	confirmed := defaultVal
	err := runForm(
		newConfirm().
			Title(label).
			Affirmative("Yes").
			Negative("No").
			Value(&confirmed),
	)
	if err != nil {
		return defaultVal, err
	}
	return confirmed, nil
}

// promptValue displays a prompt with a default value.
// Returns the entered value and any error (including ErrCancelled on ctrl+c).
func promptValue(label, defaultVal string) (string, error) {
	if !isInteractive() {
		return defaultVal, nil
	}

	value := defaultVal
	if err := runForm(huh.NewInput().Title(label).Value(&value)); err != nil {
		return defaultVal, err
	}
	return value, nil
}

// defaultField pairs a form-bound value with a fallback. After the
// form runs, Resolved returns the user's input or the fallback if
// they left it blank (accepting the placeholder).
type defaultField struct {
	value    string
	fallback string
}

// Resolved returns the user's input, or the fallback if blank.
func (f *defaultField) Resolved() string {
	if f.value == "" {
		return f.fallback
	}
	return f.value
}

// newDefaultInput returns a huh.Input with fallback shown as placeholder
// (blank accepts it) and a field to resolve the result after the form
// runs. Callers can chain .Title(), .Description(), etc. on the
// returned input.
func newDefaultInput(fallback string) (*huh.Input, *defaultField) {
	f := &defaultField{fallback: fallback}
	return huh.NewInput().Placeholder(fallback).Value(&f.value), f
}

// promptDefault displays a prompt with a default shown as placeholder text.
// Empty input accepts the default. Returns the resolved value.
func promptDefault(label, fallback string) (string, error) {
	if !isInteractive() {
		return fallback, nil
	}

	input, field := newDefaultInput(fallback)
	rewind := ui.NewCard(ui.CardInput, label).Tight().PrintRewindable()
	if err := runForm(input); err != nil {
		return fallback, err
	}
	rewind()
	return field.Resolved(), nil
}
