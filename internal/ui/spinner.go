package ui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// spinnerModel is a bubbletea model that shows a spinner while a task runs.
type spinnerModel struct {
	spinner  spinner.Model
	message  string
	done     bool
	err      error
	resultCh <-chan error
}

type taskDoneMsg struct{ err error }

func newSpinnerModel(message string, resultCh <-chan error) spinnerModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return spinnerModel{
		spinner:  s,
		message:  message,
		resultCh: resultCh,
	}
}

func (m spinnerModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForResult())
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case taskDoneMsg:
		m.done = true
		m.err = msg.err
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.done = true
			m.err = fmt.Errorf("interrupted")
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m spinnerModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	return tea.NewView(m.spinner.View() + " " + m.message + "\n")
}

func (m spinnerModel) waitForResult() tea.Cmd {
	return func() tea.Msg {
		return taskDoneMsg{err: <-m.resultCh}
	}
}

// WithSpinner runs fn while displaying a spinner with the given message.
// Returns the error from fn.
func WithSpinner(message string, fn func() error) error {
	return defaultReporter.Task(message, fn)
}

// minSpinnerDuration is the floor on how long a spinner-driven
// BubbleTea program runs before it tears down. BubbleTea v2 emits
// terminal-mode-query escape sequences during its setup; if the
// program quits before those queries are answered and consumed,
// the escapes leak into the terminal output. 100ms is enough cycles
// for the queries to round-trip without being noticeable on
// genuinely fast operations.
const minSpinnerDuration = 100 * time.Millisecond

// holdSpinner blocks until at least minSpinnerDuration has elapsed
// since start. Call from the end of spinner-driving goroutines —
// before sending the result onto the channel that triggers tea.Quit —
// so the BubbleTea program survives long enough to consume its own
// setup escapes.
func holdSpinner(start time.Time) {
	if d := time.Since(start); d < minSpinnerDuration {
		time.Sleep(minSpinnerDuration - d)
	}
}

// withSpinner is the original spinner implementation. It is called by
// cardReporter.Task to avoid a delegation cycle.
func withSpinner(message string, fn func() error) error {
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- fn()
	}()

	p := tea.NewProgram(newSpinnerModel(message, resultCh))
	model, err := p.Run()
	if err != nil {
		// Bubbletea failed to run (non-interactive) — wait for the task.
		return <-resultCh
	}

	m := model.(spinnerModel)
	if m.err != nil {
		return m.err
	}
	return nil
}

// WithSpinnerResult runs fn while displaying a spinner and returns both
// the result value and error.
func WithSpinnerResult[T any](message string, fn func() (T, error)) (T, error) {
	return TaskResult(defaultReporter, message, fn)
}

// SimulateSpinner prints a simple message with a brief delay for contexts
// where a real spinner isn't appropriate (e.g., very fast operations).
func SimulateSpinner(message string, d time.Duration) {
	fmt.Printf("%s %s\n", lipgloss.NewStyle().Foreground(Palette.Primary).Render(Palette.Dot), message)
	time.Sleep(d)
}
