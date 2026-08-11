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

		byKind := map[string]string{}
		for _, ev := range c.Events() {
			byKind[ev.Kind] = ev.Label
		}
		for kind, want := range map[string]string{
			CaptureHeader:   "doctor",
			CaptureSuccess:  "created EX-1",
			CaptureWarning:  "2 stale",
			CaptureInfo:     "info",
			CaptureMuted:    "muted",
			CaptureDryRun:   "would create EX-2",
			CaptureSaved:    "token",
			CaptureSelected: "services",
			CaptureDetails:  "workspace",
		} {
			if got := byKind[kind]; got != want {
				t.Errorf("%s label = %q, want %q", kind, got, want)
			}
		}
		details, _ := c.Find("workspace")
		if len(details.Items) != 2 || details.Items[0] != "branch: main" {
			t.Errorf("Details items = %v, want [branch: main issue: EX-1]", details.Items)
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
	if got := NewRawReporter(); got != Reporter(capture) {
		t.Errorf("NewRawReporter() = %T, want the installed capture", got)
	}

	restore()
	if _, ok := NewRawReporter().(*rawReporter); !ok {
		t.Error("restore() did not put the raw reporter back")
	}
}
