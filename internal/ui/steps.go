package ui

// Complete prints a completed step with a green checkmark.
func Complete(label string) { defaultReporter.Complete(label) }

// CompleteWithDetail prints a completed step with indented detail items.
func CompleteWithDetail(label string, items []string) { defaultReporter.CompleteDetail(label, items) }

// Skip prints a skipped step with a warning symbol.
func Skip(label string) { defaultReporter.Skip(label) }

// SkipValue prints a skipped step with the label as title and an
// inline value beside it — same one-line shape as CompleteValue.
// Use when the value names *why* the step was skipped or *what*
// state the step left things in (e.g., "skipped", "kept", "deferred").
func SkipValue(label, value string) { defaultReporter.SkipValue(label, value) }

// Fail prints a failed step with an error symbol.
func Fail(label string) { defaultReporter.Fail(label) }
