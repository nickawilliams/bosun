package ui

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
	// Skip marks a step as intentionally skipped.
	Skip(label string)
	// Fail marks a step as failed without aborting the command.
	Fail(label string)

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

	// --- Structured output ---

	// Details renders a block of key-value pairs. Use an empty
	// heading for a bare KV block (matching legacy ui.NewKV).
	Details(heading string, fields Fields)

	// Table returns a table builder. The caller adds rows and
	// calls Render.
	Table(columns ...Column) *Table
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

// SetDefault replaces the default reporter. Intended for tests and
// for eventual --output flags. Not thread-safe; set before any
// goroutines call the package-level helpers.
func SetDefault(r Reporter) { defaultReporter = r }
