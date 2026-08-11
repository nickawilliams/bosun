package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestCaptureReporter(t *testing.T) {
	t.Run("records every state with its value", func(t *testing.T) {
		c := NewCaptureReporter()

		c.Complete("plain")
		c.CompleteValue("version", "1.2.3")
		c.CompleteDetail("repos", []string{"api", "web"})
		c.Skip("absent")
		c.SkipValue("tracker", "(not configured)")
		c.Fail("broken")
		c.FailValue("git", "not found on PATH")

		want := []CaptureEvent{
			{Kind: CaptureComplete, Label: "plain", OK: true},
			{Kind: CaptureComplete, Label: "version", Value: "1.2.3", OK: true},
			{Kind: CaptureComplete, Label: "repos", Items: []string{"api", "web"}, OK: true},
			{Kind: CaptureSkip, Label: "absent"},
			{Kind: CaptureSkip, Label: "tracker", Value: "(not configured)"},
			{Kind: CaptureFail, Label: "broken"},
			{Kind: CaptureFail, Label: "git", Value: "not found on PATH"},
		}
		got := c.Events()
		if len(got) != len(want) {
			t.Fatalf("got %d events, want %d\n%s", len(got), len(want), c.Dump())
		}
		for i, w := range want {
			if got[i].Kind != w.Kind || got[i].Label != w.Label ||
				got[i].Value != w.Value || got[i].OK != w.OK {
				t.Errorf("event %d = %+v, want %+v", i, got[i], w)
			}
		}
		if items := got[2].Items; len(items) != 2 || items[0] != "api" {
			t.Errorf("CompleteDetail items = %v, want [api web]", items)
		}
	})

	t.Run("stamps nested events with their group", func(t *testing.T) {
		c := NewCaptureReporter()

		c.Complete("before")
		c.Group("integrations", func(g Reporter) {
			g.Complete("code host")
			g.SkipValue("notification", "(not configured)")
		})
		c.Complete("after")

		// The child shares the parent's event slice, so ordering
		// across levels is preserved and only the nested calls carry
		// the group.
		want := []struct {
			label string
			group string
		}{
			{"before", ""},
			{"integrations", ""},
			{"code host", "integrations"},
			{"notification", "integrations"},
			{"after", ""},
		}
		got := c.Events()
		if len(got) != len(want) {
			t.Fatalf("got %d events, want %d\n%s", len(got), len(want), c.Dump())
		}
		for i, w := range want {
			if got[i].Label != w.label || got[i].Group != w.group {
				t.Errorf("event %d = (%q, group %q), want (%q, group %q)",
					i, got[i].Label, got[i].Group, w.label, w.group)
			}
		}
	})

	t.Run("runs Task and Spinner functions", func(t *testing.T) {
		// Raw-mode semantics: rendering is replaced, the work isn't.
		c := NewCaptureReporter()
		taskErr := errors.New("boom")
		var ran int

		if err := c.Task("work", func() error { ran++; return taskErr }); !errors.Is(err, taskErr) {
			t.Errorf("Task error = %v, want %v", err, taskErr)
		}
		if err := c.Spinner("probe", func() error { ran++; return nil }); err != nil {
			t.Errorf("Spinner error = %v, want nil", err)
		}
		if ran != 2 {
			t.Errorf("ran %d functions, want 2", ran)
		}

		// Spinner leaves no card behind — only the Task is recorded.
		events := c.Events()
		if len(events) != 1 {
			t.Fatalf("got %d events, want 1 (Task only)\n%s", len(events), c.Dump())
		}
		if events[0].Kind != CaptureTask || events[0].OK || events[0].Value != "boom" {
			t.Errorf("task event = %+v, want a failed task carrying the error", events[0])
		}
	})

	t.Run("records the summary breakdown", func(t *testing.T) {
		c := NewCaptureReporter()
		segments := []SummarySegment{
			{Count: 7, Label: "passed"},
			{Count: 0, Label: "warnings"},
			{Count: 3, Label: "failed"},
		}

		c.Summary("10 checks", segments)

		ev := c.OfKind(CaptureSummary)
		if len(ev) != 1 {
			t.Fatalf("got %d summary events, want 1", len(ev))
		}
		if ev[0].Label != "10 checks" {
			t.Errorf("summary label = %q, want %q", ev[0].Label, "10 checks")
		}
		// Zero-count segments are dropped from the rendered value but
		// kept in Segments so callers can assert on counts directly.
		if ev[0].Value != "7 passed, 3 failed" {
			t.Errorf("summary value = %q, want %q", ev[0].Value, "7 passed, 3 failed")
		}
		if len(ev[0].Segments) != 3 {
			t.Errorf("summary segments = %d, want 3 (verbatim)", len(ev[0].Segments))
		}
	})

	t.Run("Find returns the first match and reports misses", func(t *testing.T) {
		c := NewCaptureReporter()
		c.CompleteValue("git", "first")
		c.CompleteValue("git", "second")

		ev, ok := c.Find("git")
		if !ok || ev.Value != "first" {
			t.Errorf("Find(git) = (%+v, %v), want the first match", ev, ok)
		}
		if _, ok := c.Find("absent"); ok {
			t.Error("Find(absent) reported a match")
		}
	})

	t.Run("Reset clears recorded events", func(t *testing.T) {
		c := NewCaptureReporter()
		c.Complete("before reset")

		c.Reset()
		c.Complete("after reset")

		got := c.Events()
		if len(got) != 1 || got[0].Label != "after reset" {
			t.Errorf("events after Reset = %+v, want only the later call", got)
		}
	})

	t.Run("Dump renders events for failure messages", func(t *testing.T) {
		c := NewCaptureReporter()
		c.Group("environment", func(g Reporter) {
			g.FailValue("global config", "not found at /nowhere")
		})
		c.CompleteDetail("repos", []string{"api"})

		dump := c.Dump()
		for _, want := range []string{
			"group environment",
			"fail[environment] global config · not found at /nowhere",
			"complete repos",
			"\tapi",
		} {
			if !strings.Contains(dump, want) {
				t.Errorf("Dump() = %q, want it to contain %q", dump, want)
			}
		}
	})

	t.Run("multi-line values collapse in Dump", func(t *testing.T) {
		// The notification check reports one line per channel; Dump is
		// one line per event, so embedded newlines are folded.
		c := NewCaptureReporter()
		c.CompleteValue("notification", "slack → bot\n#reviews\n#releases")

		if got := c.Dump(); !strings.Contains(got, "slack → bot | #reviews | #releases") {
			t.Errorf("Dump() = %q, want the value folded onto one line", got)
		}
	})

	t.Run("free-form messages expand their format", func(t *testing.T) {
		// Asserted positionally rather than by kind: the three
		// Selected* variants all record CaptureSelected, so a
		// kind-keyed lookup would only ever check the last one and
		// the other two could record anything at all.
		c := NewCaptureReporter()

		c.Header("doctor", "system check")
		c.Success("created %s", "EX-1")
		c.Warning("%d stale", 2)
		c.Info("info")
		c.Muted("muted")
		c.DryRun("would create %s", "EX-2")
		c.Saved("token", "***")
		c.Selected("repository", "api")
		c.SelectedIdentifier("branch", "story/EX-1")
		c.SelectedMulti("services", []string{"api", "web"})
		c.Details("workspace", NewFields("branch", "main", "issue", "EX-1"))

		want := []CaptureEvent{
			{Kind: CaptureHeader, Label: "doctor"},
			{Kind: CaptureSuccess, Label: "created EX-1"},
			{Kind: CaptureWarning, Label: "2 stale"},
			{Kind: CaptureInfo, Label: "info"},
			{Kind: CaptureMuted, Label: "muted"},
			{Kind: CaptureDryRun, Label: "would create EX-2"},
			{Kind: CaptureSaved, Label: "token", Value: "***"},
			{Kind: CaptureSelected, Label: "repository", Value: "api"},
			{Kind: CaptureSelected, Label: "branch", Value: "story/EX-1"},
			{Kind: CaptureSelected, Label: "services"},
			{Kind: CaptureDetails, Label: "workspace"},
		}
		got := c.Events()
		if len(got) != len(want) {
			t.Fatalf("got %d events, want %d\n%s", len(got), len(want), c.Dump())
		}
		for i, w := range want {
			if got[i].Kind != w.Kind || got[i].Label != w.Label || got[i].Value != w.Value {
				t.Errorf("event %d = (%s %q · %q), want (%s %q · %q)",
					i, got[i].Kind, got[i].Label, got[i].Value, w.Kind, w.Label, w.Value)
			}
		}

		// Items-carrying calls: Header's context strings, the
		// multi-select's values, and Details' key: value pairs.
		if items := got[0].Items; len(items) != 1 || items[0] != "system check" {
			t.Errorf("Header items = %v, want [system check]", items)
		}
		if items := got[9].Items; len(items) != 2 || items[0] != "api" {
			t.Errorf("SelectedMulti items = %v, want [api web]", items)
		}
		if items := got[10].Items; len(items) != 2 || items[0] != "branch: main" {
			t.Errorf("Details items = %v, want [branch: main issue: EX-1]", items)
		}
	})

	t.Run("records the alignment width of value forms", func(t *testing.T) {
		// The shared value column is invisible outside a rendered
		// terminal; recording it is what lets command tests assert
		// that sibling rows line up.
		c := NewCaptureReporter()

		c.CompleteValue("aligned", "v", 12)
		c.SkipValue("skipped", "why", 12)
		c.FailValue("failed", "why", 12)
		c.CompleteValue("natural", "v")

		got := c.Events()
		for i, want := range []int{12, 12, 12, 0} {
			if got[i].Align != want {
				t.Errorf("event %d (%s) align = %d, want %d",
					i, got[i].Label, got[i].Align, want)
			}
		}
	})
}

func TestIsRawCoversTheCaptureReporter(t *testing.T) {
	// The capture reporter has to count as raw: the direct-output
	// paths that check IsRaw must stay suppressed under it exactly as
	// they are under the plain raw reporter.
	prev := defaultReporter
	t.Cleanup(func() { defaultReporter = prev })

	SetDefault(NewCaptureReporter())
	if !IsRaw() {
		t.Error("IsRaw() = false for a CaptureReporter, want true")
	}
}

func TestSetRawReporterFactory(t *testing.T) {
	capture := NewCaptureReporter()

	restore := SetRawReporterFactory(func() Reporter { return capture })
	// Registered as well as called below: a t.Fatalf between here and
	// the explicit restore would otherwise leak the capture into
	// rawFactory for every later test in the package.
	t.Cleanup(restore)

	if got := NewRawReporter(); got != Reporter(capture) {
		t.Errorf("NewRawReporter() = %T, want the installed capture", got)
	}

	restore()
	if _, ok := NewRawReporter().(*rawReporter); !ok {
		t.Error("restore() did not put the raw reporter back")
	}
}
