package ui_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// TestSetContextPlainReporterEmitsHeader locks SetContext in plain
// mode: a non-empty command emits a header line; an empty command
// (EnsureHeader's call) stays silent rather than emitting a blank
// line (regression: the guard was missing and plainReporter.Header("")
// wrote "\n" to stdout).
func TestSetContextPlainReporterEmitsHeader(t *testing.T) {
	t.Run("non-empty command emits header", func(t *testing.T) {
		out := new(bytes.Buffer)
		ui.SetStreams(nil, out, new(bytes.Buffer))
		t.Cleanup(ui.ResetStreams)

		r := ui.NewPlainReporter()
		ui.SetDefault(r)
		defer ui.SetDefault(ui.NewPlainReporter()) // restore for next subtest
		ui.ResetContext()
		t.Cleanup(ui.ResetContext)

		ui.SetContext("myproject", "ISSUE-1", "review")

		got := out.String()
		if !strings.Contains(got, "review") {
			t.Errorf("SetContext: expected header containing command, got %q", got)
		}
		if !strings.Contains(got, "myproject") {
			t.Errorf("SetContext: expected header containing project, got %q", got)
		}
	})

	t.Run("empty command (EnsureHeader) stays silent", func(t *testing.T) {
		out := new(bytes.Buffer)
		ui.SetStreams(nil, out, new(bytes.Buffer))
		t.Cleanup(ui.ResetStreams)

		r := ui.NewPlainReporter()
		ui.SetDefault(r)
		defer ui.SetDefault(ui.NewPlainReporter())
		ui.ResetContext()
		t.Cleanup(ui.ResetContext)

		ui.EnsureHeader()

		if got := out.String(); got != "" {
			t.Errorf("EnsureHeader in plain mode: expected no output, got %q", got)
		}
	})
}

// TestEnsureContextIdempotent locks EnsureContext's idempotency: a
// second call after the header is already rendered is a no-op.
func TestEnsureContextIdempotent(t *testing.T) {
	out := new(bytes.Buffer)
	ui.SetStreams(nil, out, new(bytes.Buffer))
	t.Cleanup(ui.ResetStreams)

	r := ui.NewPlainReporter()
	ui.SetDefault(r)
	defer ui.SetDefault(ui.NewPlainReporter())
	ui.ResetContext()
	t.Cleanup(ui.ResetContext)

	ui.EnsureContext("proj", "", "review")
	first := out.String()

	ui.EnsureContext("proj", "", "review")
	second := out.String()

	if first != second {
		t.Errorf("EnsureContext is not idempotent: first=%q second=%q", first, second)
	}
}
