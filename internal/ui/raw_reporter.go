package ui

// rawReporter is the Reporter used in raw / machine-readable mode.
// It suppresses all timeline rendering so commands can write
// structured data directly to stdout without interference.
// Task and Group still execute their functions — they just don't
// animate or render cards.
type rawReporter struct{}

// rawMode marks rawReporter as a non-rendering Reporter — see IsRaw.
func (r *rawReporter) rawMode() {}

// rawFactory constructs the Reporter installed for raw mode. Held in
// a var because the CLI bootstrap installs a fresh raw Reporter on
// every command run: a test that wants to observe reporter calls
// (CaptureReporter) has to replace the constructor, not the installed
// value, or its reporter is overwritten as soon as the command runs.
var rawFactory = func() Reporter { return &rawReporter{} }

// NewRawReporter creates a Reporter that suppresses timeline output.
func NewRawReporter() Reporter { return rawFactory() }

// SetRawReporterFactory replaces the raw-mode Reporter constructor
// and returns a function restoring the previous one. Test-only seam —
// production code has exactly one raw Reporter.
func SetRawReporterFactory(f func() Reporter) func() {
	prev := rawFactory
	rawFactory = f
	return func() { rawFactory = prev }
}

func (r *rawReporter) Header(_ string, _ ...string)        {}
func (r *rawReporter) Complete(_ string)                   {}
func (r *rawReporter) CompleteDetail(_ string, _ []string) {}
func (r *rawReporter) CompleteValue(_, _ string, _ ...int) {}
func (r *rawReporter) Skip(_ string)                       {}
func (r *rawReporter) SkipValue(_, _ string, _ ...int)     {}
func (r *rawReporter) Fail(_ string)                       {}
func (r *rawReporter) FailValue(_, _ string, _ ...int)     {}
func (r *rawReporter) Success(_ string, _ ...any)    {}
func (r *rawReporter) Warning(_ string, _ ...any)    {}
func (r *rawReporter) Info(_ string, _ ...any)       {}
func (r *rawReporter) Muted(_ string, _ ...any)      {}
func (r *rawReporter) DryRun(_ string, _ ...any)     {}
func (r *rawReporter) Saved(_ string, _ string)      {}
func (r *rawReporter) Selected(_ string, _ string)           {}
func (r *rawReporter) SelectedIdentifier(_ string, _ string) {}
func (r *rawReporter) SelectedMulti(_ string, _ []string)    {}
func (r *rawReporter) Details(_ string, _ Fields)           {}
func (r *rawReporter) Summary(_ string, _ []SummarySegment) {}

// Task runs fn synchronously without a spinner. The function still
// executes — raw mode suppresses rendering, not behavior.
func (r *rawReporter) Task(_ string, fn func() error) error {
	return fn()
}

// Spinner runs fn synchronously without an indicator — matches Task's
// raw-mode behavior of preserving execution while suppressing UI.
func (r *rawReporter) Spinner(_ string, fn func() error) error {
	return fn()
}

// Group runs fn synchronously without animation. The inner Reporter
// is another rawReporter so nested emissions are also suppressed.
func (r *rawReporter) Group(_ string, fn func(g Reporter)) {
	fn(r)
}
