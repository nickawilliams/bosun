package testharness

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// recordFatalf swaps h.fatalf for a recorder and returns the accessor.
// Harness.Run reports starvation through h.fatalf, which is normally
// t.Fatalf — these tests need to provoke that report and then assert
// on it, which a real Fatalf's runtime.Goexit would prevent.
func recordFatalf(h *Harness) func() string {
	var msg string
	h.fatalf = func(format string, args ...any) {
		msg = fmt.Sprintf(format, args...)
	}
	return func() string { return msg }
}

// underfed shortens the starvation grace so a case that deliberately
// hangs costs a fraction of a second rather than the full production
// grace. The behaviour under test is the same either way.
func underfed(h *Harness) {
	h.stdin.starveAfter = testGrace
}

// The end-to-end shape of #59: a command that reaches a prompt the
// scenario never fed used to burn the package's whole test timeout and
// take every other result in the package with it. It must now fail —
// fast, and with a report that says which run stalled and where.
func TestRunReportsStarvationInsteadOfHanging(t *testing.T) {
	h := New(t)
	underfed(h)
	report := recordFatalf(h)

	// init is all prompts, so a run with no Type calls at all starves
	// on the first one. It writes relative to the CWD and needs no
	// existing project — see the harness README.
	h.Workspace.Uninitialize()
	t.Chdir(h.Workspace.Dir)

	start := time.Now()
	_ = h.Run("init", "--quick")
	elapsed := time.Since(start)

	got := report()
	if got == "" {
		t.Fatal("an unfed run produced no starvation report")
	}
	// Not asserted: which of the report's "how far it got" sections
	// appears. init's first prompt is reached before anything is
	// reported or printed, but ui's spacer state is a package global
	// the harness does not reset, so a second harness run in the same
	// process flushes a pending `│` into this run's output and the
	// nothing-on-screen branch stops firing. Under -count=1 nothing
	// notices; under -count=2 an assertion on it fails. That leak is
	// pre-existing and out of scope here.
	for _, want := range []string{
		"bosun init --quick", // which invocation stalled
		"never fed",          // what went wrong
		"0 input(s) queued",  // how far the scenario got
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}

	// The point of the exercise: bounded, not "until -timeout".
	if elapsed > 30*time.Second {
		t.Errorf("starved run took %s; it should fail shortly after the grace", elapsed)
	}
}

// A partially-fed scenario is the realistic failure — the author fed
// most prompts and missed one — so the report has to say how far the
// input got, and quote the output that names the prompt it stopped on.
func TestStarvationReportQuotesTheBlockedPrompt(t *testing.T) {
	h := New(t)
	underfed(h)
	report := recordFatalf(h)

	h.Workspace.Uninitialize()
	t.Chdir(h.Workspace.Dir)

	h.Type("\r") // repository patterns; everything after this is unfed

	_ = h.Run("init", "--quick")

	got := report()
	if got == "" {
		t.Fatal("an underfed run produced no starvation report")
	}
	if !strings.Contains(got, "1 input(s) queued") {
		t.Errorf("report should name the one queued input:\n%s", got)
	}
	// The command got far enough to report a step and print a timeline,
	// so both "how far it got" sections must carry their content
	// rather than the header alone.
	for _, want := range []string{
		"Last steps reported before it blocked:\n    ",
		"Last output before it blocked",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("report should quote output with ANSI stripped:\n%q", got)
	}
}

// A fully-fed run must not trip the watchdog. This is the false
// positive that would matter: every prompt's read loop parks on the
// drained queue as its form tears down, and it is the session bump at
// form exit — not the queue — that has to retire those. The grace here
// is shortened to a fraction of production's, so a run that leaned on
// the timer instead would fail this.
//
// Two prompts and no tripwire, which is also the point: the trailing
// sentinel the suites carry (see the harness README) exists only
// because an unplanned prompt used to hang, and is no longer what
// makes a scenario like this safe.
func TestRunDoesNotReportStarvationWhenFullyFed(t *testing.T) {
	h := New(t)
	underfed(h)
	report := recordFatalf(h)

	h.Workspace.Uninitialize()
	t.Chdir(h.Workspace.Dir)

	h.Type("src/*\r")     // repository patterns
	h.Type("worktrees\r") // workspace root

	if err := h.Run("init", "--quick", "--dry-run"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if got := report(); got != "" {
		t.Fatalf("a fully-fed run reported starvation:\n%s", got)
	}
}

func TestStripANSI(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"sgr", "\x1b[31mred\x1b[0m", "red"},
		{"cursor moves", "\x1b[2A\x1b[Jgone", "gone"},
		{"osc bel", "\x1b]0;title\x07body", "body"},
		{"osc st", "\x1b]8;;http://x\x1b\\link", "link"},
		{"two byte", "\x1b(Bplain", "plain"},
		{"trailing esc", "text\x1b", "text"},
		{"truncated csi", "text\x1b[31", "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSI(tc.in); got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestOutputTail(t *testing.T) {
	in := "one\n\ntwo\n   \nthree\nfour\n"
	got := outputTail(in, 2)
	want := "    three\n    four\n"
	if got != want {
		t.Errorf("outputTail = %q, want %q", got, want)
	}
	if got := outputTail("\n  \n", 3); got != "" {
		t.Errorf("outputTail of blank output = %q, want empty", got)
	}
}
