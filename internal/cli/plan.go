package cli

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// ErrCancelled is returned when the user cancels or interrupts.
var ErrCancelled = errors.New("cancelled")

// errPlanCancelled marks a cancellation the plan card has already
// rendered — the Cancelled-state card is on screen, so HandleError
// suppresses its trailing "user cancelled" card (which would say the
// same thing twice) while the non-zero exit and abort semantics are
// preserved. Wraps ErrCancelled so every existing errors.Is check
// still matches.
var errPlanCancelled = fmt.Errorf("plan %w", ErrCancelled)

// PlanAction is one step of a plan: the closure that performs it plus
// the gating that decides whether it runs behind a failure. Aliased
// rather than redeclared so the CLI and the card runner share one
// notion of a queued action.
type PlanAction = ui.PlanAction

// PlanOpts controls the two independent axes of Plan Card behavior.
type PlanOpts struct {
	// Confirm enables the interactive confirmation gate between
	// proposed and apply. Default: on for Phase.Plan commands (lifecycle);
	// off for direct mutating Tasks (workspace).
	Confirm bool

	// Apply enables execution of actions after the proposed state.
	// When false the plan renders in proposed state and returns
	// ErrCancelled without mutating. --dry-run sets this to false.
	Apply bool
}

// DefaultPlanOpts returns the standard axes for lifecycle commands:
// confirmation on, apply gated by --dry-run.
func DefaultPlanOpts(cmd *cobra.Command) PlanOpts {
	return PlanOpts{
		Confirm: true,
		Apply:   !isDryRun(cmd),
	}
}

// runPlanCard orchestrates the full plan lifecycle: proposed → confirm →
// applying (with spinner) → final state. Returns nil on success,
// ErrCancelled on dry-run/cancel/interrupt, or the first execution error.
func runPlanCard(cmd *cobra.Command, plan *ui.Plan, actions []PlanAction, opts PlanOpts) error {
	if plan.IsEmpty() {
		return nil
	}

	pc := ui.NewPlanCard(plan)

	// All items are no-ops — nothing to do. PlanVerified is a sibling
	// of PlanSuccess (same ✓ glyph + success color) with a different
	// title word — "Success" carries an action-verb feel that misreads
	// when no work was performed; "Verified" affirms the check happened
	// and the state already matches what was wanted. When the only
	// rows are failed assessments, though, nothing was verified —
	// render the failure state (the caller returns the assess error).
	if !plan.HasChanges() {
		if plan.HasFailures() {
			pc.SetState(ui.PlanFailure)
		} else {
			pc.SetState(ui.PlanVerified)
		}
		pc.Print()
		return nil
	}

	// Apply disabled (--dry-run): show proposed, exit.
	if !opts.Apply {
		pc.Print()
		return ErrCancelled
	}

	// No confirmation gate: straight to apply.
	if !opts.Confirm || isAutoApprove(cmd) {
		return applyPlanCard(pc, actions)
	}

	// Confirmation required but we can't surface a form to a human:
	// either stdin can't be read (CI / piped input) or stdout can't
	// render the prompt visibly (piped stdout). Require --approve.
	if !isInteractive() || !ui.CanRenderInteractively() {
		return fmt.Errorf("confirmation required (pass --approve, or --dry-run to preview)")
	}

	// Interactive confirmation gate. The gate is not a card of its
	// own: the plan card IS the prompt, holding its position while
	// its title row cycles Pending → Applying → outcome, with only
	// the Approve/Cancel buttons live beneath it. Rewinding the card
	// before the next state prints is what keeps every stage in the
	// one position.
	//
	// A plan taller than the screen can't cycle with its rows inside
	// the live frame: bubbletea's inline renderer drops the top of
	// an oversized frame and cursor math over it lands offset (the
	// fittedSelectHeight failure mode, #69/#98). There the rows
	// commit to scrollback as their own block and the same card
	// cycles compactly beneath them — the title row still carries
	// every stage.
	if !planGateFits(pc) {
		plan.Print()
		pc.Compact()
	}
	rewind := pc.PrintRewindable()

	// Ctrl+c interrupt: don't rewind — the abandoned question stays
	// on screen and HandleError owns the record.
	var confirmed bool
	if err := runForm(newPlanConfirm(&confirmed)); err != nil {
		return ErrCancelled
	}

	rewind()

	if !confirmed {
		// The Cancelled card IS the cancellation record, in the
		// prompt's own position — errPlanCancelled tells HandleError
		// not to add another.
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return errPlanCancelled
	}

	return applyPlanCard(pc, actions)
}

// planGateChrome is how many terminal rows the confirmation gate
// occupies beyond the plan card itself: the three rows huh's confirm
// paints (the button row, a separator, and the help line), the
// connector row above them, and one row of slack so an off-by-one in
// any of them still leaves the whole frame on screen. Measured
// against formFirstFrame — see TestPlanGateChromeFitsTheFrame, which
// fails if huh's chrome ever grows.
const planGateChrome = 5

// planGateFits reports whether the plan card can carry its rows
// through the gate's live frame — the in-place cycle — or whether
// the plan is tall enough that the rows must commit to scrollback
// first and the card cycle compactly. Mirrors fittedSelectHeight's
// bound: the whole frame has to fit on screen or the inline renderer
// corrupts it.
func planGateFits(pc *ui.PlanCard) bool {
	lines := strings.Count(strings.TrimSuffix(pc.Render(), "\n"), "\n") + 1
	return lines+planGateChrome <= ui.TermHeight()
}

// applyPlanCard runs actions with an animated spinner, transitioning the
// card through applying → success/partial/failure.
func applyPlanCard(pc *ui.PlanCard, actions []PlanAction) error {
	return pc.RunApply(actions)
}

// newPlanConfirm builds the bare Approve/Cancel field mounted
// beneath the plan card while it waits in Pending. No title: the
// card's title row directly above already carries the plan's scale.
// Both runPlanCard and the demo snapshot route through this so the
// two renders can't drift.
func newPlanConfirm(confirmed *bool) *huh.Confirm {
	return newConfirm().
		Affirmative("Approve").
		Negative("Cancel").
		Value(confirmed)
}

// isAutoApprove reports whether the run pre-approved plan
// confirmation. Only --approve counts: --force is the SAFETY bypass
// (dirty trees, readiness blockers) and deliberately does not answer
// the plan prompt — "I accept the data risk" and "apply this plan"
// are two separate consents.
func isAutoApprove(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("approve")
	return v
}
