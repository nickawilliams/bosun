package cli

import (
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// TestApplyTextareaFocusMarker is the canary for the reflect+unsafe
// bridge in applyTextareaFocusMarker: it pins the assumption that
// huh.Text keeps its textarea in an unexported field named "textarea"
// of type textarea.Model. If a huh bump changes that, this fails
// loudly — the alternative is the focus marker silently vanishing from
// every textarea. On failure: check huh's field_text.go for the new
// shape, or replace the bridge with huh's native prompt API if one
// gained it.
//
// The attach succeeding isn't enough: a huh bump that keeps the field
// but re-applies its own Prompt/SetPromptFunc after construction would
// pass the attach and still drop the marker — exactly the silent
// failure this canary exists to catch. So the second half renders a
// frame and asserts the marker actually appears.
func TestApplyTextareaFocusMarker(t *testing.T) {
	field := huh.NewText()
	if !applyTextareaFocusMarker(field) {
		t.Fatal("applyTextareaFocusMarker no longer attaches — huh's internal textarea field changed; see test doc for next steps")
	}

	form := huh.NewForm(huh.NewGroup(field)).WithWidth(60)
	form.Init()
	if view := form.View(); !strings.Contains(view, ui.FocusMarker) {
		t.Fatalf("rendered textarea frame lacks the %q focus marker — huh re-applied its own prompt after attach?\n%s",
			ui.FocusMarker, view)
	}
}
