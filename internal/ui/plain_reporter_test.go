package ui_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// plainOut captures what a plainReporter writes to defaultOutput.
// plainErr captures what it writes to defaultErr.
func withPlainReporter(t *testing.T) (r ui.Reporter, out *bytes.Buffer, errOut *bytes.Buffer) {
	t.Helper()
	out = new(bytes.Buffer)
	errOut = new(bytes.Buffer)
	ui.SetStreams(nil, out, errOut)
	t.Cleanup(func() { ui.ResetStreams() })
	r = ui.NewPlainReporter()
	return r, out, errOut
}

func TestPlainReporter_Complete(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Complete("Set up workspace")
	got := out.String()
	if !strings.Contains(got, "[ok]") {
		t.Errorf("Complete: missing [ok] in %q", got)
	}
	if !strings.Contains(got, "Set up workspace") {
		t.Errorf("Complete: missing label in %q", got)
	}
}

func TestPlainReporter_CompleteValue(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.CompleteValue("Create branch", "feature-123")
	got := out.String()
	if !strings.Contains(got, "[ok]") {
		t.Errorf("CompleteValue: missing [ok] in %q", got)
	}
	if !strings.Contains(got, "feature-123") {
		t.Errorf("CompleteValue: missing value in %q", got)
	}
}

func TestPlainReporter_Skip(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Skip("Link to Jira")
	got := out.String()
	if !strings.Contains(got, "[skip]") {
		t.Errorf("Skip: missing [skip] in %q", got)
	}
}

func TestPlainReporter_SkipValue(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.SkipValue("Link to Jira", "not configured")
	got := out.String()
	if !strings.Contains(got, "[skip]") {
		t.Errorf("SkipValue: missing [skip] in %q", got)
	}
	if !strings.Contains(got, "not configured") {
		t.Errorf("SkipValue: missing value in %q", got)
	}
}

func TestPlainReporter_Fail(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Fail("Run tests")
	got := out.String()
	if !strings.Contains(got, "[fail]") {
		t.Errorf("Fail: missing [fail] in %q", got)
	}
}

func TestPlainReporter_FailValue(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.FailValue("Run tests", "exit code 1")
	got := out.String()
	if !strings.Contains(got, "[fail]") {
		t.Errorf("FailValue: missing [fail] in %q", got)
	}
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("FailValue: missing value in %q", got)
	}
}

func TestPlainReporter_Warning_GoesToStderr(t *testing.T) {
	r, out, errOut := withPlainReporter(t)
	r.Warning("deprecated configuration")
	if errOut.Len() == 0 {
		t.Error("Warning: expected stderr output, got none")
	}
	if out.Len() != 0 {
		t.Errorf("Warning: expected no stdout output, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "deprecated configuration") {
		t.Errorf("Warning: missing message in stderr %q", errOut.String())
	}
}

func TestPlainReporter_Task_Success(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	err := r.Task("Compile", func() error { return nil })
	if err != nil {
		t.Fatalf("Task: unexpected error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[ok]") {
		t.Errorf("Task success: missing [ok] in %q", got)
	}
	if !strings.Contains(got, "Compile") {
		t.Errorf("Task success: missing title in %q", got)
	}
}

func TestPlainReporter_Task_Failure(t *testing.T) {
	r, out, _ := withPlainReporter(t)

	taskErr := fmt.Errorf("exit code 1")
	err := r.Task("Compile", func() error { return taskErr })
	if err == nil {
		t.Fatal("Task: expected error, got nil")
	}
	got := out.String()
	if !strings.Contains(got, "[fail]") {
		t.Errorf("Task failure: missing [fail] in %q", got)
	}
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("Task failure: missing error in %q", got)
	}
}

func TestPlainReporter_Spinner_RunsFn(t *testing.T) {
	r, _, _ := withPlainReporter(t)
	ran := false
	_ = r.Spinner("Waiting", func() error {
		ran = true
		return nil
	})
	if !ran {
		t.Error("Spinner: fn was not executed")
	}
}

func TestPlainReporter_Group_RunsFnAndEmitsChildren(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Group("Checks", func(g ui.Reporter) {
		g.Complete("Git configured")
		g.SkipValue("Jira", "not configured")
	})
	got := out.String()
	if !strings.Contains(got, "Git configured") {
		t.Errorf("Group: missing child output in %q", got)
	}
	if !strings.Contains(got, "Jira") {
		t.Errorf("Group: missing second child in %q", got)
	}
}

func TestPlainReporter_Details(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Details("Config", ui.NewFields("key", "value", "path", "/etc/bosun"))
	got := out.String()
	if !strings.Contains(got, "Config") {
		t.Errorf("Details: missing heading in %q", got)
	}
	if !strings.Contains(got, "key: value") {
		t.Errorf("Details: missing field in %q", got)
	}
}

func TestPlainReporter_Details_Empty(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Details("Config", ui.NewFields())
	if out.Len() != 0 {
		t.Errorf("Details empty: expected no output, got %q", out.String())
	}
}

func TestPlainReporter_Summary(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Summary("3 checks", []ui.SummarySegment{
		{Count: 2, Label: "passed"},
		{Count: 1, Label: "failed"},
	})
	got := out.String()
	if !strings.Contains(got, "3 checks") {
		t.Errorf("Summary: missing total in %q", got)
	}
	if !strings.Contains(got, "2 passed") {
		t.Errorf("Summary: missing passed segment in %q", got)
	}
	if !strings.Contains(got, "1 failed") {
		t.Errorf("Summary: missing failed segment in %q", got)
	}
}

func TestPlainReporter_Summary_ZeroSegmentsOmitted(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Summary("3 checks", []ui.SummarySegment{
		{Count: 3, Label: "passed"},
		{Count: 0, Label: "failed"},
	})
	got := out.String()
	if strings.Contains(got, "failed") {
		t.Errorf("Summary: zero segment should be omitted, got %q", got)
	}
}

func TestPlainReporter_Header(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Header("doctor")
	if !strings.Contains(out.String(), "doctor") {
		t.Errorf("Header: missing command in %q", out.String())
	}
}

func TestPlainReporter_Header_WithContext(t *testing.T) {
	r, out, _ := withPlainReporter(t)
	r.Header("start", "BOSUN-123", "my-workspace")
	got := out.String()
	if !strings.Contains(got, "start") {
		t.Errorf("Header: missing command in %q", got)
	}
	if !strings.Contains(got, "BOSUN-123") {
		t.Errorf("Header: missing context in %q", got)
	}
}

func TestIsRaw_PlainReporter(t *testing.T) {
	prev := ui.Default()
	t.Cleanup(func() { ui.SetDefault(prev) })

	ui.SetDefault(ui.NewPlainReporter())
	if !ui.IsRaw() {
		t.Error("IsRaw() = false for plainReporter, want true")
	}
}
