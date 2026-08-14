package cli

import (
	"errors"

	"github.com/nickawilliams/bosun/internal/ui"
)

// HandleError renders a fatal command error to the user, choosing the
// output mode (raw vs comfy) and closing the timeline as needed.
// ErrCancelled is surfaced as a skipped card rather than a failure.
// Relies on main()'s Bootstrap having already established the UI
// mode (raw vs. card) and config — so errors arriving from cobra's
// ValidateArgs or fang's flag parsing render in the same style as
// errors from RunE.
func HandleError(err error) {
	if errors.Is(err, errPlanCancelled) {
		// The plan card already rendered its Cancelled state — a
		// trailing "user cancelled" card would say the same thing
		// twice. Just close the timeline.
		if !ui.IsRaw() {
			ui.EndTimeline()
		}
		return
	}
	if errors.Is(err, ErrCancelled) {
		if ui.IsRaw() {
			// Route through the Reporter so plainReporter emits a skip
			// line; rawReporter's Skip is a no-op, which is correct for
			// machine-readable mode.
			ui.Default().Skip("user cancelled")
		} else {
			renderErrorHeader()
			ui.NewCard(ui.CardSkipped, "user cancelled").Print()
			ui.EndTimeline()
		}
		return
	}
	if ui.IsRaw() {
		ui.Error("%s", err.Error())
		return
	}
	renderErrorHeader()
	ui.NewCard(ui.CardFailed, err.Error()).TitleColor(ui.Palette.Error).Print()
	ui.EndTimeline()
}
