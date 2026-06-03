package ui

import "image/color"

// Reporter is the semantic output surface commands render through.
// An implementation decides how to present each call — card timeline,
// JSON, silent, test capture, etc. The interface contains only the
// methods in frequent use today; rare helpers (Error, Bold, Item)
// stay as package-level functions.
type Reporter interface {
	// Header opens a command run. The first argument is the command
	// name (e.g., "start"); optional context strings are runtime
	// values like an issue key or workspace name, shown as subtitle.
	Header(command string, context ...string)

	// --- Terminal step states ---

	// Complete marks a step as successfully finished.
	Complete(label string)
	// CompleteDetail marks a step as complete and lists indented
	// detail items beneath it.
	CompleteDetail(label string, items []string)
	// CompleteValue marks a step as complete with a brief inline
	// value, rendered as "label · value" on one line — label in the
	// title style, value muted. Use when a step's result is a single
	// concise datum (a version, a path, a resolved identifier).
	// Multi-line values are supported: the first line renders
	// inline, subsequent lines align under the first value character.
	// An optional alignWidth pads the label to a fixed visual column
	// so sibling cards line up; 0 (or omitted) means natural width.
	CompleteValue(label, value string, alignWidth ...int)
	// Skip marks a step as intentionally skipped.
	Skip(label string)
	// SkipValue marks a step as skipped with a brief inline reason,
	// rendered in the same shape as CompleteValue.
	SkipValue(label, value string, alignWidth ...int)
	// Fail marks a step as failed without aborting the command.
	Fail(label string)
	// FailValue marks a step as failed with a brief inline reason,
	// rendered in the same shape as CompleteValue.
	FailValue(label, value string, alignWidth ...int)

	// --- Free-form messages ---

	// Success prints a positive confirmation line (fmt-style).
	Success(format string, args ...any)
	// Warning prints a cautionary message to stderr (fmt-style).
	Warning(format string, args ...any)
	// Info prints an informational line (fmt-style).
	Info(format string, args ...any)
	// Muted prints a dimmed secondary line (fmt-style).
	Muted(format string, args ...any)

	// --- Mode indicators ---

	// DryRun prints a dry-run notice (fmt-style).
	DryRun(format string, args ...any)
	// Saved prints feedback that a value was persisted.
	Saved(label, value string)
	// Selected prints feedback that a single value was chosen
	// interactively. The label is the field title and value is the
	// user's selection, rendered as a subtitle.
	Selected(label, value string)
	// SelectedMulti prints feedback that multiple values were chosen
	// interactively. The label is the field title and values are the
	// user's selections, rendered as indented detail items.
	SelectedMulti(label string, values []string)

	// --- Async tasks ---

	// Task runs fn while showing a running indicator, then
	// finalizes as success or failure. Returns fn's error.
	Task(title string, fn func() error) error

	// Spinner runs fn while showing a running indicator for the
	// named work item, then clears the indicator without emitting
	// any terminal card. The caller is responsible for emitting the
	// final state via Complete / Fail / Skip (or their value-form
	// variants). Use when the work's result has a non-default
	// rendering shape that doesn't fit Task's auto-success /
	// auto-failure card.
	Spinner(title string, fn func() error) error

	// --- Grouped output ---

	// Group renders a Timeline Card with children. The parent header
	// shows pending while fn runs; emissions on the inner Reporter
	// appear indented under the parent in real-time. When fn returns,
	// the parent finalizes to a state aggregated from its children
	// (failure dominates; all-skipped → skipped; success+skipped →
	// success; info doesn't propagate).
	Group(title string, fn func(g Reporter))

	// --- Structured output ---

	// Details renders a Data Card: a heading with key-value body,
	// no status glyph. Empty fields are suppressed entirely.
	Details(heading string, fields Fields)

	// Summary renders an end-of-run rollup card: muted total head
	// followed by a comma-joined colored breakdown of non-zero
	// segments. The card glyph color is the color of the *last*
	// non-zero segment, so order segments ascending by severity for
	// the worst case to dominate. Use for the summary line at the
	// end of multi-step operations (status, doctor, lifecycle).
	Summary(total string, segments []SummarySegment)
}

// SummarySegment is one entry in a Summary card's colored breakdown.
// Segments render in the order provided, comma-separated, non-zero
// only. The Color is applied both to the segment's text and to the
// card glyph if this is the last non-zero segment.
type SummarySegment struct {
	Count int
	Label string
	Color color.Color
}

// Fields is an ordered list of key-value pairs. Ordered so that
// column-width alignment is stable and JSON serialization is
// deterministic.
type Fields []Field

// Field is a single key-value pair.
type Field struct {
	Key   string
	Value string
}

// NewFields constructs Fields from variadic "key", "value" strings.
// An odd trailing element is silently dropped.
func NewFields(pairs ...string) Fields {
	f := make(Fields, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		f = append(f, Field{Key: pairs[i], Value: pairs[i+1]})
	}
	return f
}

// TaskResult runs fn while showing a running indicator and returns
// both the result value and the error. It is a free function because
// Go interfaces cannot have generic methods. Delegates to r.Task
// internally so swapping reporters automatically swaps rendering.
func TaskResult[T any](r Reporter, title string, fn func() (T, error)) (T, error) {
	var val T
	err := r.Task(title, func() error {
		v, e := fn()
		val = v
		return e
	})
	return val, err
}

// defaultReporter is the Reporter used by package-level helpers
// (Success, Header, WithSpinner, etc.) so existing call sites
// delegate through the interface without any code changes.
var defaultReporter Reporter = newCardReporter()

// Default returns the package-level default Reporter.
func Default() Reporter { return defaultReporter }

// IsRaw reports whether the default Reporter is a raw (non-rendering)
// variant. Used by Card.Print and other direct-output functions to
// suppress timeline rendering in raw mode.
func IsRaw() bool {
	_, ok := defaultReporter.(*rawReporter)
	return ok
}

// SetDefault replaces the default reporter. Intended for tests and
// for eventual --output flags. Not thread-safe; set before any
// goroutines call the package-level helpers.
func SetDefault(r Reporter) { defaultReporter = r }
