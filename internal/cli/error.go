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
	if errors.Is(err, ErrCancelled) {
		renderErrorHeader()
		ui.NewCard(ui.CardSkipped, "user cancelled").Print()
		if !ui.IsRaw() {
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
