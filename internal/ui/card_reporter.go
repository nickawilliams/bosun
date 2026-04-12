package ui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
)

// cardReporter is the default Reporter implementation. For v1 it
// faithfully reproduces the legacy rendering from output.go, steps.go,
// and spinner.go so there is zero visual change when the delegation
// wiring lands. A future version will route through the Card timeline
// primitives.
type cardReporter struct{}

func newCardReporter() Reporter { return &cardReporter{} }

// Header reproduces the ● command context format from output.go:78-84.
func (r *cardReporter) Header(command string, context ...string) {
	parts := []string{boldStyle.Render(command)}
	for _, c := range context {
		parts = append(parts, primaryStyle.Render(c))
	}
	symbol := lipgloss.NewStyle().Foreground(Palette.Accent).Render("●")
	fmt.Printf("\n%s %s\n\n", symbol, strings.Join(parts, " "))
}

// Complete reproduces steps.go:18-19.
func (r *cardReporter) Complete(label string) {
	fmt.Printf("  %s %s\n", stepCheckStyle.Render(Palette.Check), label)
}

// CompleteDetail reproduces steps.go:23-27.
func (r *cardReporter) CompleteDetail(label string, items []string) {
	r.Complete(label)
	for _, item := range items {
		fmt.Printf("      %s %s\n", stepArrowStyle.Render(Palette.Arrow), stepItemStyle.Render(item))
	}
}

// Skip reproduces steps.go:31-32.
func (r *cardReporter) Skip(label string) {
	fmt.Printf("  %s %s\n", stepSkipStyle.Render("!"), label)
}

// Fail reproduces steps.go:36-37.
func (r *cardReporter) Fail(label string) {
	fmt.Printf("  %s %s\n", stepFailStyle.Render(Palette.Cross), label)
}

// Success reproduces output.go:21-23.
func (r *cardReporter) Success(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Println(successStyle.Render(Palette.Check) + " " + text)
}

// Warning reproduces output.go:33-35 (writes to stderr).
func (r *cardReporter) Warning(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, warningStyle.Render("!")+" "+text)
}

// Info reproduces output.go:39-41.
func (r *cardReporter) Info(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Println(primaryStyle.Render(Palette.Bullet) + " " + text)
}

// Muted reproduces output.go:45-47.
func (r *cardReporter) Muted(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	fmt.Println(mutedStyle.Render(text))
}

// DryRun reproduces output.go:88-91.
func (r *cardReporter) DryRun(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	prefix := warningStyle.Render("[dry-run]")
	fmt.Printf("%s %s\n", prefix, text)
}

// Saved reproduces output.go:66-73.
func (r *cardReporter) Saved(label, value string) {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(Palette.Primary)
	valueStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	fmt.Printf("  %s %s\n    %s\n",
		stepCheckStyle.Render(Palette.Check),
		titleStyle.Render(label),
		valueStyle.Render(value),
	)
}

// Task reproduces spinner.go:75-93 (the non-card bubbletea spinner).
func (r *cardReporter) Task(title string, fn func() error) error {
	return withSpinner(title, fn)
}

// Details renders key-value pairs. An empty heading produces a bare
// KV block matching the legacy ui.NewKV() output; a non-empty heading
// renders the heading as an Info line above the block.
func (r *cardReporter) Details(heading string, fields Fields) {
	if heading != "" {
		r.Info("%s", heading)
	}
	kv := NewKV()
	for _, f := range fields {
		kv.Add(f.Key, f.Value)
	}
	kv.Print()
}

// Table returns a new table builder, same as ui.NewTable.
func (r *cardReporter) Table(columns ...Column) *Table {
	return NewTable(columns...)
}
