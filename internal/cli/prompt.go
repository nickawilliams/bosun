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

// recordExcerptLines is how many lines of a multi-line answer the
// post-submit record keeps (via ui.Excerpt) before collapsing the rest
// into a "… +K lines" marker. Shared by every record shape that can
// carry textarea input (create's KV card, typeaheadText's Selected).
const recordExcerptLines = 3

// transformFieldTitle normalizes a form-field title to the app's
// title-case convention so labels stay consistent across forms
// regardless of how the caller wrote them. Wrap a label with
// ui.PreserveCase to opt out — useful for acronyms, brand names,
// or other casing that shouldn't be normalized.
func transformFieldTitle(s string) string {
	if rest, verbatim := ui.StripPreserveCase(s); verbatim {
		return rest
	}
	return ui.TitleCase(s)
}

// newInput is the cli's standard huh.Input constructor. Same as
// huh.NewInput().Title(title) but the title is normalized via
// transformFieldTitle so casing is consistent across forms, and the
// prompt uses the shared focus marker. Use instead of huh.NewInput
// when setting a Title.
func newInput(title string) *huh.Input {
	return huh.NewInput().Title(transformFieldTitle(title)).Prompt(ui.FocusMarker)
}

// rawInput is huh.NewInput() with only the shared focus-marker prompt
// applied — for inputs that don't set a Title through newInput (secret
// fields, value-only prompts) but still need the consistent cursor.
func rawInput() *huh.Input {
	return huh.NewInput().Prompt(ui.FocusMarker)
}

// newText — see newInput. All textareas route through here (or
// rawText) so the focus marker (and any future textarea-wide behavior)
// lands in one place. The `❭` marker comes via
// applyTextareaFocusMarker — a guarded bridge around huh v2.0.3's
// missing textarea-prompt API; see its doc for the mechanism and the
// exit plan.
func newText(title string) *huh.Text {
	t := huh.NewText().Title(transformFieldTitle(title))
	applyTextareaFocusMarker(t)
	return t
}

// rawText is huh.NewText() without a Title — for textareas whose title
// lives on a wrapping card (typeahead editors). Carries the same focus
// marker as newText.
func rawText() *huh.Text {
	t := huh.NewText()
	applyTextareaFocusMarker(t)
	return t
}

// newSelect — see newInput. Generic over the option value type.
func newSelect[T comparable](title string) *huh.Select[T] {
	return huh.NewSelect[T]().Title(transformFieldTitle(title))
}

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

// promptForValues gates VALUE prompts — the ones that fill in a field
// the command could resolve on its own from flags, config, or
// detection. Commands have two distinct interactivity classes and this
// names the line between them:
//
//   - Scope and approval gates (which repos to act on, the plan
//     confirmation) show whenever the session is interactive, because
//     they ask permission rather than asking for data. --approve opts
//     out of those.
//   - Value prompts are OFF by default — the resolved value is meant to
//     be right without asking — and --interactive turns them on.
//
// The two classes are independent on purpose: --approve answers "apply
// this plan", not "take every default", so `--interactive --approve`
// still prompts for values and still skips the plan gate. Same
// separate-consents principle as isAutoApprove vs. --force.
//
// Currently an alias for forceInteractive; it exists so each call site
// declares WHICH class it belongs to, and so the policy has one place
// to change.
func promptForValues(cmd *cobra.Command) bool {
	return forceInteractive(cmd)
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
	// Announce form exit on every return path: a read loop this form
	// leaks (bubbletea can't cancel reads on non-File inputs) must not
	// be able to consume input meant for a later prompt in the same
	// run. See ui.ReleaseInput / ui.InputHandoff.
	defer ui.ReleaseInput()
	prologueLines := emitFormPrologue(fields)
	err := buildForm(fields).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		// BubbleTea leaves a trailing blank line on exit; move the
		// cursor up so the cancel card (or its comfy connector)
		// overwrites it rather than creating double spacing.
		_, _ = fmt.Fprint(ui.Output(), "\x1b[A")
		return ErrCancelled
	}
	// Undo the prologue lines so any wrapping PrintRewindable's rewind
	// closure sees the cursor in the same position as if no form had
	// run. Without this, multi-field forms leave their prologue spacer
	// above the form's render area, and the rewind (which moves up by
	// the card's line count only) can't reach back over it — leaving
	// a stranded spacer above the card that the next emit then stacks
	// against.
	if prologueLines > 0 {
		_, _ = fmt.Fprintf(ui.Output(), "\x1b[%dF\x1b[J", prologueLines)
	}
	return err
}

// snapshotForm renders a form as a static visual snapshot — used by
// the demo command to illustrate form layouts without requiring an
// interactive session. Shares its prologue (leading spacer) and
// construction (theme, layout, help, I/O) with runForm, so the
// snapshot stays in visual parity with what a real interactive run
// produces. Skips runForm's interactivity guard since snapshots are
// intentionally non-interactive.
//
// Unlike runForm, the snapshot doesn't undo the prologue — the
// caller wants the leading spacer visible in the captured output,
// not cleaned up.
func snapshotForm(fields ...huh.Field) {
	_ = emitFormPrologue(fields)
	_, _ = fmt.Fprint(ui.Output(), formFirstFrame(fields...))
	_, _ = fmt.Fprint(ui.Output(), "\n\n")
}

// formFirstFrame renders the exact first frame runForm's program will
// paint for fields — buildForm + Init + View, the same construction
// path — for seamless-takeover flows: paint this as the previous
// program's final frame, cursor-up over it, and huh's first repaint is
// byte-identical (no flash). Deterministic for cursor-less fields
// (selects, multi-selects); text inputs blink a cursor, so takeover
// callers should stick to select-shaped forms.
func formFirstFrame(fields ...huh.Field) string {
	f := buildForm(fields)
	_ = f.Init()
	return f.View()
}

// gatherFrameChrome is how many terminal rows a gather's selection
// frame occupies beyond its option rows: the section spacer and the
// input-card header the steps program paints above the form (one row
// each), the two rows huh's field adds below the options (a separator
// and the help line), and one row of slack so an off-by-one in any of
// them still leaves the whole frame on screen. Measured against
// formFirstFrame — see TestFittedSelectHeightFitsTheFrame, which
// fails if huh's chrome ever grows.
const gatherFrameChrome = 5

// minSelectHeight is the shortest option list worth rendering. A
// terminal too short for even this can't be served correctly, so the
// floor keeps the field usable (huh scrolls within it) instead of
// collapsing it to nothing.
const minSelectHeight = 3

// fittedSelectHeight returns the height a gather's selection list
// should be given: its full length, unless that would paint a frame
// taller than the terminal.
//
// Full height is the wanted default — the submitted form is replaced
// by a record card listing the same rows, so matching the two makes
// the swap read as in-place rather than an expand/collapse. But an
// oversized frame isn't merely ugly, it's broken: bubbletea's inline
// renderer drops the top of a frame taller than the screen, so the
// header and the first options never paint, and the cursor-up that
// hands the frame over to huh (formFirstFrame's takeover) then moves
// against rows that aren't there — the repaint lands offset and the
// post-submit erase eats real output above. Capping trades the
// in-place swap for a scrolling viewport (huh's own, first-class)
// exactly when the in-place swap was unachievable anyway.
//
// Inert whenever the list fits: the frame, and every byte of the
// takeover arithmetic, is unchanged.
func fittedSelectHeight(options int) int {
	fit := ui.TermHeight() - gatherFrameChrome
	if fit < minSelectHeight {
		fit = minSelectHeight
	}
	return min(options, fit)
}

// buildForm constructs a huh.Form with the app's theme, layout,
// help, and I/O bindings. Single source of truth so any change to
// the form's chrome (theme, layout, help, width) flows to every
// caller — interactive runs via runForm and static snapshots via
// snapshotForm — without manual parity maintenance.
//
// WithWidth is critical when the output isn't a real terminal —
// huh's bubbletea program never receives a resize message in that
// case and otherwise crashes inside textinput on a 0-width slice.
func buildForm(fields []huh.Field) *huh.Form {
	return huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(formTheme).
		WithLayout(ui.NewTimelineLayout()).
		WithShowHelp(true).
		WithInput(ui.Input()).
		WithOutput(ui.Output()).
		WithWidth(ui.TermWidth())
}

// emitFormPrologue emits the leading visual break before a form
// renders. Multi-field forms get an unconditional recessed │ row
// so the form's first field doesn't sit flush against any heading
// above it (Tight cards intentionally clear needsSpacer, so the
// default FlushSpacer path doesn't help here). Single-field forms
// stay on the existing FlushSpacer behavior — only emit if one is
// pending — since a leading row would create awkward whitespace
// above a bare Confirm with no description.
//
// Returns the number of stdout lines actually written so runForm can
// undo them on exit, restoring the cursor to its pre-runForm position
// for any wrapping rewind.
func emitFormPrologue(fields []huh.Field) int {
	if len(fields) > 1 {
		ui.ClearSpacer()
		_, _ = fmt.Fprintln(ui.Output(), " "+lipgloss.NewStyle().Foreground(ui.Palette.Recessed).Render("│"))
		ui.RequestSpacer()
		return 1
	}
	ui.FlushSpacer()
	return 0
}

// newConfirm creates a huh Confirm field with app defaults (left-aligned buttons).
func newConfirm() *huh.Confirm {
	return huh.NewConfirm().WithButtonAlignment(lipgloss.Left)
}

// promptRequired prompts for a value if stdin is a terminal. If stdin is not
// a terminal (piped/scripted), returns empty string and the caller should
// error.
//
// Wraps the input in a rewindable CardInput header so the spacer state
// resets cleanly on submit — passing the label as the form's own Title
// would orphan the prologue `│` after huh clears its content, leaving a
// double-spacer above the next card.
func promptRequired(label string) string {
	if !isInteractive() {
		return ""
	}

	var value string
	rewind := ui.NewCard(ui.CardInput, label).Tight().PrintRewindable()
	if err := runForm(rawInput().Value(&value)); err != nil {
		return ""
	}
	rewind()
	return value
}

// promptConfirm shows a yes/no confirmation as a Dialog with the
// label as its title. Returns the user's choice (or the default in
// non-interactive mode). Returns ErrCancelled if the user presses
// ctrl+c.
func promptConfirm(label string, defaultVal bool) (bool, error) {
	return NewDialog(label).Default(defaultVal).Show()
}

// promptIntegrationGate shows the gate form for one integration: a
// single Select widget over the provider Options with "Skip" merged
// in as the first (default-selected) entry. Returns (provider,
// skipped, err).
//
// Merging Skip into the same widget as the provider choices means
// the form has exactly one input — Skip is the default focus and
// hitting Enter immediately skips, preserving the fluid "tap Enter
// through the integrations I don't care about" rhythm. Picking any
// other option commits to that provider and proceeds to the
// per-field silent resolve loop. The empty string is used as the
// Skip sentinel because real provider names are always non-empty.
//
// Non-interactive mode returns ("", true, nil) — no prompt to
// render against, so the safest path is to skip.
//
// Ctrl+c surfaces as the underlying form's error (typically
// ErrCancelled). Form residue stays on screen so the caller's
// cancellation card reads as a response to the abandoned question.
func promptIntegrationGate(label string, providerKey ConfigKey) (provider string, skipped bool, err error) {
	if !isInteractive() {
		return "", true, nil
	}

	opts := make([]huh.Option[string], 0, len(providerKey.Options)+1)
	// "- skip -" with surrounding dashes signals the option as a
	// meta-action rather than a concrete provider — distinguishes it
	// from any hypothetical provider that might literally be named
	// "skip" and reads as a structural choice ("don't configure
	// this") next to the data-shaped provider rows below it.
	opts = append(opts, huh.NewOption("- skip -", ""))
	for _, p := range providerKey.Options {
		opts = append(opts, huh.NewOption(p, p))
	}

	choice := "" // Default-select Skip via the empty sentinel.

	rewind := ui.NewCard(ui.CardInput, label).AccentBody().Tight().PrintRewindable()
	formErr := runForm(
		huh.NewSelect[string]().Options(opts...).Value(&choice),
	)

	if formErr != nil {
		ui.RequestSpacer()
		return "", false, formErr
	}

	rewind()
	if choice == "" {
		return "", true, nil
	}
	return choice, false, nil
}

// promptValue displays a prompt with a default value.
// Returns the entered value and any error (including ErrCancelled on ctrl+c).
func promptValue(label, defaultVal string) (string, error) {
	if !isInteractive() {
		return defaultVal, nil
	}

	value := defaultVal
	if err := runForm(huh.NewInput().Title(label).Prompt(ui.FocusMarker).Value(&value)); err != nil {
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

// Entry returns the raw text the user typed — empty when they accepted
// the placeholder. Prefer it over Resolved when the fallback isn't a
// literal the caller wants frozen in (a per-item default, say): the
// caller can then keep resolving each item itself.
func (f *defaultField) Entry() string { return f.value }

// newDefaultInput returns a huh.Input with fallback shown as placeholder
// (blank accepts it) and a field to resolve the result after the form
// runs. Callers can chain .Title(), .Description(), etc. on the
// returned input.
func newDefaultInput(fallback string) (*huh.Input, *defaultField) {
	f := &defaultField{fallback: fallback}
	return huh.NewInput().Placeholder(fallback).Prompt(ui.FocusMarker).Value(&f.value), f
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
