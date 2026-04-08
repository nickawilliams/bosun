package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(Theme.Primary)
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

func (m spinnerModel) View() string {
	if m.done {
		return ""
	}
	return m.spinner.View() + " " + m.message + "\n"
}

func (m spinnerModel) waitForResult() tea.Cmd {
	return func() tea.Msg {
		return taskDoneMsg{err: <-m.resultCh}
	}
}

// WithSpinner runs fn while displaying a spinner with the given message.
// Returns the error from fn. If the terminal doesn't support the spinner
// (e.g., non-interactive), it falls back to a simple message.
func WithSpinner(message string, fn func() error) error {
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
	type result struct {
		val T
		err error
	}

	resCh := make(chan result, 1)
	errCh := make(chan error, 1)

	go func() {
		val, err := fn()
		resCh <- result{val, err}
		errCh <- err
	}()

	p := tea.NewProgram(newSpinnerModel(message, errCh))
	_, err := p.Run()
	if err != nil {
		// Bubbletea failed — wait for the task.
		r := <-resCh
		return r.val, r.err
	}

	r := <-resCh
	return r.val, r.err
}

// SimulateSpinner prints a simple message with a brief delay for contexts
// where a real spinner isn't appropriate (e.g., very fast operations).
func SimulateSpinner(message string, d time.Duration) {
	fmt.Printf("%s %s\n", lipgloss.NewStyle().Foreground(Theme.Primary).Render(Theme.Dot), message)
	time.Sleep(d)
}
