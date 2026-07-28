package cli

import (
	"testing"

	"charm.land/huh/v2"
)

// TestApplyTextareaFocusMarker is the canary for the reflect+unsafe
// bridge in applyTextareaFocusMarker: it pins the assumption that
// huh.Text keeps its textarea in an unexported field named "textarea"
// of type textarea.Model. If a huh bump changes that, this fails
// loudly — the alternative is the focus marker silently vanishing from
// every textarea. On failure: check huh's field_text.go for the new
// shape, or replace the bridge with huh's native prompt API if one
// gained it.
func TestApplyTextareaFocusMarker(t *testing.T) {
	if !applyTextareaFocusMarker(huh.NewText()) {
		t.Fatal("applyTextareaFocusMarker no longer attaches — huh's internal textarea field changed; see test doc for next steps")
	}
}
