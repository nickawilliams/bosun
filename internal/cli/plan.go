package cli

import (
	"errors"
	"fmt"

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

	// Interactive confirmation gate — the house single-card morph:
	// one Pending card carries the plan rows, and only the
	// Approve/Cancel buttons are live beneath it. The rows can't sit
	// inside the live form frame — a plan scales with data, and an
	// inline BubbleTea frame taller than the terminal drops its top
	// rows and corrupts cursor math (the fittedSelectHeight failure
	// mode, #69/#98) — so the card commits straight to scrollback
	// and the live frame stays a few rows regardless of plan size.
	// Ctrl+c interrupt: bail; HandleError owns the record.
	pc.PrintCommitted()

	var confirmed bool
	if err := runForm(newPlanConfirm(&confirmed)); err != nil {
		return ErrCancelled
	}

	if !confirmed {
		// The committed Pending rows above plus this compact card
		// ARE the cancellation record — errPlanCancelled tells
		// HandleError not to add another. Compact survives SetState
		// (only setFinalState clears it): a cancel changes no rows,
		// so none need repeating.
		pc.Compact()
		pc.SetState(ui.PlanCancelled)
		pc.Print()
		return errPlanCancelled
	}

	// The apply continues the cycle compactly in the buttons'
	// position — the Pending record above already lists every row.
	// An imperfect outcome brings the rows back (see
	// PlanCard.Compact).
	pc.Compact()
	return applyPlanCard(pc, actions)
}

// applyPlanCard runs actions with an animated spinner, transitioning the
// card through applying → success/partial/failure.
func applyPlanCard(pc *ui.PlanCard, actions []PlanAction) error {
	return pc.RunApply(actions)
}

// newPlanConfirm builds the bare Approve/Cancel field mounted
// beneath the committed Pending card. No title: the card's title row
// already carries the plan's scale and its rows sit directly above.
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
