package cli

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// bufferedTerm points the UI streams at a buffer, which makes
// ui.TermHeight answer with its 24-row default instead of whatever
// terminal the suite happens to run under.
func bufferedTerm(t *testing.T) {
	t.Helper()
	ui.SetStreams(nil, &bytes.Buffer{}, nil)
	t.Cleanup(ui.ResetStreams)
}

func TestFittedSelectHeight(t *testing.T) {
	bufferedTerm(t)
	fit := ui.TermHeight() - gatherFrameChrome

	t.Run("a list that fits keeps its full height", func(t *testing.T) {
		// The takeover's in-place swap depends on this: cap a form that
		// would have fitted and the record card replacing it grows,
		// turning the swap into an expand.
		if got := fittedSelectHeight(fit); got != fit {
			t.Errorf("fittedSelectHeight(%d) = %d, want the full list", fit, got)
		}
		if got := fittedSelectHeight(1); got != 1 {
			t.Errorf("fittedSelectHeight(1) = %d, want 1", got)
		}
	})

	t.Run("a longer list is capped to what fits", func(t *testing.T) {
		if got := fittedSelectHeight(fit + 1); got != fit {
			t.Errorf("fittedSelectHeight(%d) = %d, want the %d that fit", fit+1, got, fit)
		}
		if got := fittedSelectHeight(500); got != fit {
			t.Errorf("fittedSelectHeight(500) = %d, want the %d that fit", got, fit)
		}
	})

	t.Run("a terminal too short for the chrome still gets a usable field", func(t *testing.T) {
		// TermHeight can't be driven below its default from here, so
		// exercise the floor directly: whatever the arithmetic yields,
		// the field never collapses to nothing.
		if minSelectHeight < 1 {
			t.Fatalf("minSelectHeight = %d, want a usable floor", minSelectHeight)
		}
		if got := fittedSelectHeight(1000); got < minSelectHeight {
			t.Errorf("fittedSelectHeight(1000) = %d, want at least the %d floor", got, minSelectHeight)
		}
	})
}

// TestFittedSelectHeightFitsTheFrame is the assertion gatherFrameChrome
// is a measurement of, not a guess: a fitted list must produce a frame
// — spacer, header, options, and huh's own trailing rows — that fits on
// screen. It fails if huh's chrome grows, which is exactly when the
// constant needs revisiting; without it the takeover would go back to
// repainting against rows the renderer had dropped.
func TestFittedSelectHeightFitsTheFrame(t *testing.T) {
	bufferedTerm(t)

	const options = 500 // far more than any terminal holds
	opts := make([]huh.Option[string], options)
	for i := range opts {
		opts[i] = huh.NewOption("repository · service-"+strconv.Itoa(i), strconv.Itoa(i))
	}
	var picked []string
	frame := formFirstFrame(fittedMultiSelect(opts, &picked))
	if !strings.HasSuffix(frame, "\n") {
		frame += "\n"
	}

	// What the steps program paints as its final frame: the section
	// spacer, the Tight input-card header, then the form.
	header := ui.NewCard(ui.CardInput, "services").Tight().Render()
	rows := 1 + strings.Count(header, "\n") + strings.Count(frame, "\n")

	if rows > ui.TermHeight() {
		t.Errorf("fitted frame is %d rows on a %d-row terminal — gatherFrameChrome (%d) no longer covers huh's chrome",
			rows, ui.TermHeight(), gatherFrameChrome)
	}
}

// TestFittedSelectHeightFitsThePickerFrame is the same measurement
// for the picker shape — a single-select beneath a slot's Tight
// input-card header (pickOrPromptWorkspace, pickOrPromptIssue) —
// since huh.Select's chrome isn't guaranteed to match MultiSelect's.
// An unbounded picker frame taller than the screen leaves rows the
// inline renderer never tracked, so the post-submit erase strands a
// truncated copy of the list in scrollback (#98).
func TestFittedSelectHeightFitsThePickerFrame(t *testing.T) {
	bufferedTerm(t)

	const options = 500 // far more than any terminal holds
	opts := make([]huh.Option[string], options)
	for i := range opts {
		opts[i] = huh.NewOption("feature/EX-"+strconv.Itoa(i)+"_workspace", strconv.Itoa(i))
	}
	var picked string
	frame := formFirstFrame(fittedSelect(opts, &picked))
	if !strings.HasSuffix(frame, "\n") {
		frame += "\n"
	}

	header := ui.NewCard(ui.CardInput, "select workspace").Tight().Render()
	rows := 1 + strings.Count(header, "\n") + strings.Count(frame, "\n")

	if rows > ui.TermHeight() {
		t.Errorf("fitted picker frame is %d rows on a %d-row terminal — gatherFrameChrome (%d) doesn't cover huh.Select's chrome",
			rows, ui.TermHeight(), gatherFrameChrome)
	}
}

func TestTransformFieldTitle(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"sentence case normalized", "repository patterns", "Repository Patterns"},
		{"mixed case left alone for words with uppercase", "repository GitHub patterns", "Repository GitHub Patterns"},
		{"already title-cased is idempotent", "Workspace Root", "Workspace Root"},
		{"acronym preserved by titleCase", "API key", "API Key"},
		{"verbatim opt-out via ui.PreserveCase", ui.PreserveCase("API key"), "API key"},
		{"verbatim works on already-correct strings", ui.PreserveCase("GitHub"), "GitHub"},
		{"empty string stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transformFieldTitle(tc.input)
			if got != tc.want {
				t.Errorf("transformFieldTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
