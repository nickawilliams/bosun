package ui

import (
	"fmt"
	"strings"
)

// plainReporter is the Reporter used when stdout is not a terminal but
// no machine-readable output was requested. It emits plain, unstyled
// lines so that piped, redirected, and CI contexts see the same
// semantic events as an interactive run — without ANSI escape codes or
// the bubbletea animation that requires a real TTY.
//
// Output format: a fixed-width status prefix followed by the label,
// optionally ": value" appended inline.
//
//   [ok]   Set up workspace
//   [ok]   Create branch: feature-123
//   [skip] Link to Jira: not configured
//   [fail] Run tests: exit code 1
//
// All lines go to stdout except Warning, which goes to stderr.
// Group nesting is flattened — each child emits its own status line;
// the group title is printed once as a section header so the output
// stays legible even without indentation.
type plainReporter struct{}

// plainFactory constructs the Reporter installed for plain (non-TTY)
// mode. Held in a var so tests can replace it via
// SetPlainReporterFactory — the same seam rawFactory uses.
var plainFactory = func() Reporter { return &plainReporter{} }

// NewPlainReporter creates a Reporter that emits plain, unstyled lines.
func NewPlainReporter() Reporter { return plainFactory() }

// SetPlainReporterFactory replaces the plain-mode Reporter constructor
// and returns a function restoring the previous one. Test-only seam.
func SetPlainReporterFactory(f func() Reporter) func() {
	prev := plainFactory
	plainFactory = f
	return func() { plainFactory = prev }
}

const (
	prefixOK   = "[ok]   "
	prefixSkip = "[skip] "
	prefixFail = "[fail] "
	prefixInfo = "[info] "
	prefixWarn = "[warn] "
	prefixDry  = "[dry]  "
	// continuationPad aligns lines that continue after the first.
	continuationPad = "       "
)

func (r *plainReporter) writeln(format string, args ...any) {
	fmt.Fprintf(defaultOutput, format+"\n", args...)
}

func (r *plainReporter) warnln(format string, args ...any) {
	fmt.Fprintf(defaultErr, format+"\n", args...)
}

// plainLine writes a status-prefixed line, appending ": value" when
// value is non-empty.
func (r *plainReporter) line(prefix, label, value string) {
	if value == "" {
		r.writeln("%s%s", prefix, label)
		return
	}
	r.writeln("%s%s: %s", prefix, label, value)
}

// plainLines writes a status-prefixed line followed by indented detail
// items.
func (r *plainReporter) lines(prefix, label string, items []string) {
	r.writeln("%s%s", prefix, label)
	for _, it := range items {
		r.writeln("%s%s", continuationPad, it)
	}
}

// Header prints a brief command identification line so CI logs show
// which command ran. Context strings (issue key, workspace) are joined
// with " · ".
func (r *plainReporter) Header(command string, context ...string) {
	if len(context) == 0 {
		r.writeln("%s", command)
		return
	}
	r.writeln("%s: %s", command, strings.Join(context, " · "))
}

func (r *plainReporter) Complete(label string) {
	r.line(prefixOK, label, "")
}

func (r *plainReporter) CompleteDetail(label string, items []string) {
	r.lines(prefixOK, label, items)
}

func (r *plainReporter) CompleteValue(label, value string, _ ...int) {
	r.line(prefixOK, label, value)
}

func (r *plainReporter) Skip(label string) {
	r.line(prefixSkip, label, "")
}

func (r *plainReporter) SkipValue(label, value string, _ ...int) {
	r.line(prefixSkip, label, value)
}

func (r *plainReporter) Fail(label string) {
	r.line(prefixFail, label, "")
}

func (r *plainReporter) FailValue(label, value string, _ ...int) {
	r.line(prefixFail, label, value)
}

func (r *plainReporter) Success(format string, args ...any) {
	r.line(prefixOK, fmt.Sprintf(format, args...), "")
}

func (r *plainReporter) Warning(format string, args ...any) {
	r.warnln("%s%s", prefixWarn, fmt.Sprintf(format, args...))
}

func (r *plainReporter) Info(format string, args ...any) {
	r.line(prefixInfo, fmt.Sprintf(format, args...), "")
}

func (r *plainReporter) Muted(format string, args ...any) {
	r.line(prefixInfo, fmt.Sprintf(format, args...), "")
}

func (r *plainReporter) DryRun(format string, args ...any) {
	r.line(prefixDry, fmt.Sprintf(format, args...), "")
}

func (r *plainReporter) Saved(label, value string) {
	r.line(prefixOK, label, value)
}

func (r *plainReporter) Selected(label, value string) {
	r.line(prefixOK, label, value)
}

func (r *plainReporter) SelectedIdentifier(label, value string) {
	r.line(prefixOK, label, value)
}

func (r *plainReporter) SelectedMulti(label string, values []string) {
	if len(values) == 0 {
		r.line(prefixOK, label, "(none)")
		return
	}
	r.lines(prefixOK, label, values)
}

// Task runs fn and prints a status line once it completes. On failure
// the error message is appended inline as the value.
func (r *plainReporter) Task(title string, fn func() error) error {
	err := fn()
	if err != nil {
		r.line(prefixFail, title, err.Error())
	} else {
		r.line(prefixOK, title, "")
	}
	return err
}

// Spinner runs fn without emitting a line of its own. The caller is
// responsible for emitting the final state via Complete / Fail / Skip
// (or their value forms), matching the contract defined in Reporter.
func (r *plainReporter) Spinner(_ string, fn func() error) error {
	return fn()
}

// Group prints the group title as a section header, then runs fn
// against the same reporter so each child emits its own status line.
// Children are not additionally indented — flat output is more
// friendly to log tools than whitespace-sensitive nesting.
func (r *plainReporter) Group(title string, fn func(g Reporter)) {
	r.writeln("%s", title)
	fn(r)
}

// Details prints each key-value pair on its own line under the
// heading. Empty field lists are suppressed (matching card behaviour).
func (r *plainReporter) Details(heading string, fields Fields) {
	if len(fields) == 0 {
		return
	}
	if heading == "" {
		heading = "Details"
	}
	r.writeln("%s", heading)
	for _, f := range fields {
		r.writeln("%s%s: %s", continuationPad, f.Key, f.Value)
	}
}

// Summary prints the total label followed by each non-zero segment
// in parentheses.
func (r *plainReporter) Summary(total string, segments []SummarySegment) {
	var parts []string
	for _, s := range segments {
		if s.Count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.Count, s.Label))
	}
	if len(parts) == 0 {
		r.writeln("%s", total)
		return
	}
	r.writeln("%s (%s)", total, strings.Join(parts, ", "))
}
