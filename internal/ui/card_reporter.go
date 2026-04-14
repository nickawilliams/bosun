package ui

import (
	"fmt"
	"strings"
)

// cardReporter is the default Reporter implementation. It renders
// through the Card timeline so all output participates in the
// vertical glyph spine with consistent spacing and alignment.
type cardReporter struct{}

func newCardReporter() Reporter { return &cardReporter{} }

// Header emits a CardRoot that opens the timeline for a command run.
func (r *cardReporter) Header(command string, context ...string) {
	card := NewCard(CardRoot, command)
	if len(context) > 0 {
		card.Subtitle(strings.Join(context, " · "))
	}
	card.Print()
}

// Complete emits a CardSuccess for a finished step.
func (r *cardReporter) Complete(label string) {
	NewCard(CardSuccess, label).Print()
}

// CompleteDetail emits a CardSuccess with indented detail items.
func (r *cardReporter) CompleteDetail(label string, items []string) {
	NewCard(CardSuccess, label).Muted(items...).Print()
}

// Skip emits a CardSkipped for a step that was not attempted.
func (r *cardReporter) Skip(label string) {
	NewCard(CardSkipped, label).Print()
}

// Fail emits a CardFailed for a step that errored.
func (r *cardReporter) Fail(label string) {
	NewCard(CardFailed, label).Print()
}

// Success emits a CardSuccess with the formatted message as title.
func (r *cardReporter) Success(format string, args ...any) {
	NewCard(CardSuccess, fmt.Sprintf(format, args...)).Print()
}

// Warning emits a CardSkipped (warning glyph) for cautionary messages.
func (r *cardReporter) Warning(format string, args ...any) {
	NewCard(CardSkipped, fmt.Sprintf(format, args...)).Print()
}

// Info emits a CardInfo with the formatted message as title.
func (r *cardReporter) Info(format string, args ...any) {
	NewCard(CardInfo, fmt.Sprintf(format, args...)).Print()
}

// Muted prints dimmed text in the timeline without a glyph.
func (r *cardReporter) Muted(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Print(comfyPrefix())
	fmt.Printf(" %s  %s\n", NewCard(CardInfo, "").renderConnector(), mutedStyle.Render(text))
}

// DryRun emits a CardInfo with a dry-run prefix.
func (r *cardReporter) DryRun(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	NewCard(CardInfo, fmt.Sprintf("[dry-run] %s", text)).Print()
}

// Saved emits a CardSuccess with the label as title and value as
// muted detail beneath it.
func (r *cardReporter) Saved(label, value string) {
	NewCard(CardSuccess, label).Muted(value).Print()
}

// Task runs fn while showing a spinner card, then finalizes as
// success or failure.
func (r *cardReporter) Task(title string, fn func() error) error {
	return RunCard(title, fn)
}

// Details emits a CardInfo with key-value pairs as body. An empty
// heading produces a bare KV block without a card title.
func (r *cardReporter) Details(heading string, fields Fields) {
	pairs := make([]string, 0, len(fields)*2)
	for _, f := range fields {
		pairs = append(pairs, f.Key, f.Value)
	}
	if heading == "" {
		heading = "Details"
	}
	NewCard(CardInfo, heading).KV(pairs...).Print()
}

// Table returns a new table builder.
func (r *cardReporter) Table(columns ...Column) *Table {
	return NewTable(columns...)
}
