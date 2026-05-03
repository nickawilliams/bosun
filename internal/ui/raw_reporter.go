package ui

// rawReporter is the Reporter used in raw / machine-readable mode.
// It suppresses all timeline rendering so commands can write
// structured data directly to stdout without interference.
// Task and Group still execute their functions — they just don't
// animate or render cards.
type rawReporter struct{}

// NewRawReporter creates a Reporter that suppresses timeline output.
func NewRawReporter() Reporter { return &rawReporter{} }

func (r *rawReporter) Header(_ string, _ ...string) {}
func (r *rawReporter) Complete(_ string)             {}
func (r *rawReporter) CompleteDetail(_ string, _ []string) {}
func (r *rawReporter) Skip(_ string)                 {}
func (r *rawReporter) Fail(_ string)                 {}
func (r *rawReporter) Success(_ string, _ ...any)    {}
func (r *rawReporter) Warning(_ string, _ ...any)    {}
func (r *rawReporter) Info(_ string, _ ...any)       {}
func (r *rawReporter) Muted(_ string, _ ...any)      {}
func (r *rawReporter) DryRun(_ string, _ ...any)     {}
func (r *rawReporter) Saved(_ string, _ string)      {}
func (r *rawReporter) Selected(_ string, _ string)   {}
func (r *rawReporter) SelectedMulti(_ string, _ []string) {}
func (r *rawReporter) Details(_ string, _ Fields)    {}

// Task runs fn synchronously without a spinner. The function still
// executes — raw mode suppresses rendering, not behavior.
func (r *rawReporter) Task(_ string, fn func() error) error {
	return fn()
}

// Group runs fn synchronously without animation. The inner Reporter
// is another rawReporter so nested emissions are also suppressed.
func (r *rawReporter) Group(_ string, fn func(g Reporter)) {
	fn(r)
}
