package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

// Selected emits a CardSuccess with the label as title and the chosen
// value as a subtitle. Use after an interactive single-value prompt.
func (r *cardReporter) Selected(label, value string) {
	NewCard(CardSuccess, label).Subtitle(value).Print()
}

// SelectedMulti emits a CardSuccess with the label as title and chosen
// values as indented muted items. Use after an interactive multi-select
// prompt. Prints "(none)" subtitle if values is empty.
func (r *cardReporter) SelectedMulti(label string, values []string) {
	if len(values) == 0 {
		NewCard(CardSuccess, label).Subtitle("(none)").Print()
		return
	}
	NewCard(CardSuccess, label).Muted(values...).Print()
}

// Task runs fn while showing a spinner card, then finalizes as
// success or failure.
func (r *cardReporter) Task(title string, fn func() error) error {
	return RunCard(title, fn)
}

// Group renders a Timeline Card with children via a single BubbleTea
// program that animates both parent and child spinners simultaneously.
// The callback runs in a goroutine; its Reporter emissions become
// messages that the BubbleTea model renders in real-time. After the
// callback returns, BubbleTea exits and the final static render is
// printed.
func (r *cardReporter) Group(title string, fn func(g Reporter)) {
	indentLevel := 0
	msgCh := make(chan groupMsg, 256)

	g := &group{
		outer:  r,
		title:  title,
		indent: indentLevel + 1,
		msgCh:  msgCh,
	}

	go func() {
		start := time.Now()
		fn(g)
		holdSpinner(start)
		msgCh <- groupDoneMsg{}
	}()

	fmt.Print(comfyPrefix())

	model := newGroupModel(title, indentLevel, msgCh)
	p := tea.NewProgram(model)
	final, err := p.Run()

	if err != nil {
		// Non-interactive fallback: drain messages and print directly.
		drainGroupFallback(title, indentLevel, g, msgCh)
		comfyBreak = true
		return
	}

	// BubbleTea's final View() rendered the finalized group content
	// in place (root finalized in groupDoneMsg handler), so the
	// output is already on screen. No reprint needed.
	_ = final
	comfyBreak = true
}

// drainGroupFallback handles the case where BubbleTea can't run
// (non-interactive terminal). It waits for the callback to finish
// and prints all accumulated children statically.
func drainGroupFallback(title string, indent int, g *group, msgCh <-chan groupMsg) {
	// Callback is already running in a goroutine; drain its messages.
	for msg := range msgCh {
		switch msg := msg.(type) {
		case groupChildMsg:
			fmt.Print(msg.rendered)
		case groupTaskDoneMsg:
			fmt.Print(msg.rendered)
		case groupDoneMsg:
			finalState := g.aggregate()
			fmt.Print(NewCard(finalState, title).Indent(indent).Render())
			return
		}
	}
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
