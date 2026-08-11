package ui

import (
	"fmt"
	"strings"
	"sync"
)

// Capture event kinds. One per Reporter method whose call is
// observable in rendered output; Spinner is deliberately absent
// because it emits nothing of its own (the caller reports the final
// state via Complete / Fail / Skip).
const (
	CaptureHeader   = "header"
	CaptureComplete = "complete"
	CaptureSkip     = "skip"
	CaptureFail     = "fail"
	CaptureSuccess  = "success"
	CaptureWarning  = "warning"
	CaptureInfo     = "info"
	CaptureMuted    = "muted"
	CaptureDryRun   = "dry-run"
	CaptureSaved    = "saved"
	CaptureSelected = "selected"
	CaptureTask     = "task"
	CaptureGroup    = "group"
	CaptureDetails  = "details"
	CaptureSummary  = "summary"
)

// CaptureEvent is one recorded Reporter call.
type CaptureEvent struct {
	// Kind is one of the Capture* constants above.
	Kind string
	// Group is the enclosing Group's title, empty at top level.
	Group string
	// Label is the step label, heading, or format-expanded message.
	Label string
	// Value is the inline value/reason for the *Value forms, the
	// Summary total, or the Saved/Selected value.
	Value string
	// Items are the detail lines for CompleteDetail / SelectedMulti,
	// or the "key: value" pairs for Details.
	Items []string
	// Segments carries a Summary card's breakdown verbatim.
	Segments []SummarySegment
	// OK is false for a failed Task and for the fail kind.
	OK bool
}

// CaptureReporter is a Reporter that records calls instead of
// rendering them, so tests can assert on what a command reported
// without parsing ANSI-styled terminal output.
//
// It is a raw-mode Reporter (IsRaw reports true for it), which means
// the direct-output paths that check IsRaw stay suppressed exactly as
// they would under NewRawReporter — the capture observes the semantic
// calls, it doesn't change what else the command writes.
//
// Install it in tests via SetRawReporterFactory so it survives the
// CLI bootstrap's per-run reporter install. Safe for concurrent use.
type CaptureReporter struct {
	mu     *sync.Mutex
	events *[]CaptureEvent
	group  string
}

// NewCaptureReporter constructs an empty CaptureReporter.
func NewCaptureReporter() *CaptureReporter {
	return &CaptureReporter{mu: &sync.Mutex{}, events: &[]CaptureEvent{}}
}

// rawMode marks CaptureReporter as a non-rendering Reporter — see
// IsRaw.
func (c *CaptureReporter) rawMode() {}

func (c *CaptureReporter) record(e CaptureEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.Group = c.group
	*c.events = append(*c.events, e)
}

// Events returns a snapshot of every recorded call, in order.
func (c *CaptureReporter) Events() []CaptureEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CaptureEvent, len(*c.events))
	copy(out, *c.events)
	return out
}

// Reset discards all recorded events. Used between runs so each
// command invocation is asserted in isolation.
func (c *CaptureReporter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.events = (*c.events)[:0]
}

// Find returns the first recorded event with the given label. Step
// labels are unique within a command run in practice, so this is the
// usual way to assert on one step's outcome.
func (c *CaptureReporter) Find(label string) (CaptureEvent, bool) {
	for _, e := range c.Events() {
		if e.Label == label {
			return e, true
		}
	}
	return CaptureEvent{}, false
}

// OfKind returns every recorded event of the given kind, in order.
func (c *CaptureReporter) OfKind(kind string) []CaptureEvent {
	var out []CaptureEvent
	for _, e := range c.Events() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Dump renders the recorded events as one line each. Intended for
// test failure messages, where it shows what the command actually
// reported.
func (c *CaptureReporter) Dump() string {
	var b strings.Builder
	for _, e := range c.Events() {
		b.WriteString(e.Kind)
		if e.Group != "" {
			fmt.Fprintf(&b, "[%s]", e.Group)
		}
		b.WriteString(" ")
		b.WriteString(e.Label)
		if e.Value != "" {
			b.WriteString(" · ")
			b.WriteString(strings.ReplaceAll(e.Value, "\n", " | "))
		}
		for _, it := range e.Items {
			b.WriteString("\n\t")
			b.WriteString(it)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- Reporter implementation ---

func (c *CaptureReporter) Header(command string, context ...string) {
	c.record(CaptureEvent{Kind: CaptureHeader, Label: command, Items: context, OK: true})
}

func (c *CaptureReporter) Complete(label string) {
	c.record(CaptureEvent{Kind: CaptureComplete, Label: label, OK: true})
}

func (c *CaptureReporter) CompleteDetail(label string, items []string) {
	c.record(CaptureEvent{Kind: CaptureComplete, Label: label, Items: items, OK: true})
}

func (c *CaptureReporter) CompleteValue(label, value string, _ ...int) {
	c.record(CaptureEvent{Kind: CaptureComplete, Label: label, Value: value, OK: true})
}

func (c *CaptureReporter) Skip(label string) {
	c.record(CaptureEvent{Kind: CaptureSkip, Label: label})
}

func (c *CaptureReporter) SkipValue(label, value string, _ ...int) {
	c.record(CaptureEvent{Kind: CaptureSkip, Label: label, Value: value})
}

func (c *CaptureReporter) Fail(label string) {
	c.record(CaptureEvent{Kind: CaptureFail, Label: label})
}

func (c *CaptureReporter) FailValue(label, value string, _ ...int) {
	c.record(CaptureEvent{Kind: CaptureFail, Label: label, Value: value})
}

func (c *CaptureReporter) Success(format string, args ...any) {
	c.record(CaptureEvent{Kind: CaptureSuccess, Label: fmt.Sprintf(format, args...), OK: true})
}

func (c *CaptureReporter) Warning(format string, args ...any) {
	c.record(CaptureEvent{Kind: CaptureWarning, Label: fmt.Sprintf(format, args...)})
}

func (c *CaptureReporter) Info(format string, args ...any) {
	c.record(CaptureEvent{Kind: CaptureInfo, Label: fmt.Sprintf(format, args...), OK: true})
}

func (c *CaptureReporter) Muted(format string, args ...any) {
	c.record(CaptureEvent{Kind: CaptureMuted, Label: fmt.Sprintf(format, args...), OK: true})
}

func (c *CaptureReporter) DryRun(format string, args ...any) {
	c.record(CaptureEvent{Kind: CaptureDryRun, Label: fmt.Sprintf(format, args...), OK: true})
}

func (c *CaptureReporter) Saved(label, value string) {
	c.record(CaptureEvent{Kind: CaptureSaved, Label: label, Value: value, OK: true})
}

func (c *CaptureReporter) Selected(label, value string) {
	c.record(CaptureEvent{Kind: CaptureSelected, Label: label, Value: value, OK: true})
}

func (c *CaptureReporter) SelectedIdentifier(label, value string) {
	c.record(CaptureEvent{Kind: CaptureSelected, Label: label, Value: value, OK: true})
}

func (c *CaptureReporter) SelectedMulti(label string, values []string) {
	c.record(CaptureEvent{Kind: CaptureSelected, Label: label, Items: values, OK: true})
}

// Task runs fn and records the resulting card. Raw-mode semantics:
// the work still happens, only the rendering is replaced.
func (c *CaptureReporter) Task(title string, fn func() error) error {
	err := fn()
	ev := CaptureEvent{Kind: CaptureTask, Label: title, OK: err == nil}
	if err != nil {
		ev.Value = err.Error()
	}
	c.record(ev)
	return err
}

// Spinner runs fn without recording anything of its own — the
// indicator leaves no card behind, and the caller emits the final
// state itself.
func (c *CaptureReporter) Spinner(_ string, fn func() error) error {
	return fn()
}

// Group records the group header and runs fn against a child capture
// that stamps every nested event with the group title. The child
// shares the parent's event slice, so ordering across levels is
// preserved.
func (c *CaptureReporter) Group(title string, fn func(g Reporter)) {
	c.record(CaptureEvent{Kind: CaptureGroup, Label: title, OK: true})
	fn(&CaptureReporter{mu: c.mu, events: c.events, group: title})
}

func (c *CaptureReporter) Details(heading string, fields Fields) {
	items := make([]string, 0, len(fields))
	for _, f := range fields {
		items = append(items, f.Key+": "+f.Value)
	}
	c.record(CaptureEvent{Kind: CaptureDetails, Label: heading, Items: items, OK: true})
}

func (c *CaptureReporter) Summary(total string, segments []SummarySegment) {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if s.Count == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d %s", s.Count, s.Label))
	}
	c.record(CaptureEvent{
		Kind:     CaptureSummary,
		Label:    total,
		Value:    strings.Join(parts, ", "),
		Segments: segments,
		OK:       true,
	})
}

// Verify CaptureReporter satisfies Reporter at compile time.
var _ Reporter = (*CaptureReporter)(nil)
