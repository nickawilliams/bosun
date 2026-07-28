package cli

import (
	"reflect"
	"unsafe"

	"charm.land/bubbles/v2/textarea"
	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// applyTextareaFocusMarker gives a huh.Text the same `❭` focus marker
// every other field carries, rendered on the first display row only.
//
// huh v2.0.3 hardcodes the textarea's prompt to "" and exposes no way
// to set it — but bubbles' textarea has the exact primitive we need,
// SetPromptFunc(width, func(PromptInfo) string), which is called per
// display row with {LineNumber, Focused}. The only thing in the way is
// that huh keeps its textarea in an unexported field. This bridges that
// gap with reflect+unsafe: the field's type (textarea.Model) is
// exported and the module graph resolves a single bubbles version for
// huh and us, so type identity is guaranteed; only the field *name* is
// an internal detail.
//
// Guarded, not assumed: if huh renames or retypes the field, this
// returns false and the textarea renders exactly as it does today (no
// marker) — a cosmetic regression, not a break. The canary test
// TestApplyTextareaFocusMarker pins the assumption so a future huh
// bump fails the suite loudly instead of silently dropping the marker.
// Delete all of this in favor of a huh-native prompt API when one
// exists.
//
// The marker inherits the theme's prompt styling (accent when focused)
// because bubbles renders the promptFunc result through the prompt
// style; blurred rows and continuation rows render 2 spaces, keeping
// content at the same column as before.
func applyTextareaFocusMarker(t *huh.Text) bool {
	f := reflect.ValueOf(t).Elem().FieldByName("textarea")
	if !f.IsValid() || f.Type() != reflect.TypeFor[textarea.Model]() {
		return false
	}
	ta := (*textarea.Model)(unsafe.Pointer(f.UnsafeAddr()))
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 && info.Focused {
			return ui.FocusMarker
		}
		return "  "
	})
	return true
}
