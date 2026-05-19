package cli

import (
	"errors"

	"github.com/nickawilliams/bosun/internal/ui"
)

// HandleError renders a fatal command error to the user, choosing the
// output mode (raw vs comfy) and closing the timeline as needed.
// ErrCancelled is surfaced as a skipped card rather than a failure.
func HandleError(err error) {
	if errors.Is(err, ErrCancelled) {
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
	ui.Fail(err.Error())
	ui.EndTimeline()
}
