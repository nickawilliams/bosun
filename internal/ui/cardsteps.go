package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CardStep pairs one spinner card with the work it narrates. Used
// with RunCardSteps for sequences of spinner phases that occupy the
// same timeline position.
type CardStep struct {
	Card *Card
	Run  func() error
}

// RunCardSteps runs a sequence of spinner steps inside ONE BubbleTea
// program, then renders successor() as the program's final frame.
//
// This is the structural fix for the blank-flash class of bugs: every
// tea program boundary (exit → clear → next program's first frame) is
// a visible blank seam, so any loop that runs one spinner program per
// item flickers once per item. Steps advance by swapping the view
// inside a single program — no boundaries between items — and the
// morph-style final frame means the program's exit paints the
// successor content rather than clearing to blank. Use this for any
// "spinner per item, then a result card" cycle; reserve the per-call
// runners (RunCard*, slot.Run*) for isolated one-shot spinners.
//
// Step errors halt the sequence: the failed step's card renders
// permanently in CardFailed state with the error as subtitle, and the
// error is returned with a nil rewind. Steps that want
// tolerate-and-continue semantics should capture their errors and
// return nil (the Services detection pattern).
//
// successor is evaluated after the last step completes, so it can
// derive the final card from state the steps accumulated. A nil
// successor vanishes: empty final frame, no-op rewind. On success the
// returned rewind erases the successor block (same contract as
// RunCardMorph).
func RunCardSteps(steps []CardStep, successor func() *Card) (func(), error) {
	if IsRaw() {
		for _, s := range steps {
			if err := s.Run(); err != nil {
				return nil, err
			}
		}
		return func() {}, nil
	}

	prevSpacer := needsSpacer
	prefix := spacerPrefix()

	// No steps — nothing to animate; just print the successor (or
	// nothing) and hand back its rewind.
	if len(steps) == 0 {
		needsSpacer = prevSpacer
		if successor == nil {
			return func() {}, nil
		}
		final := successor()
		rendered := prefix + final.Render()
		fmt.Print(rendered)
		lines := strings.Count(rendered, "\n")
		return func() {
			if lines > 0 {
				fmt.Printf("\x1b[%dF\x1b[J", lines)
			}
			needsSpacer = prevSpacer
		}, nil
	}

	for _, s := range steps {
		s.Card.state = CardRunning
	}

	// Worker goroutine executes steps sequentially, reporting each
	// completion. holdSpinner keeps every step's frame on screen long
	// enough to read instead of strobing through fast items.
	resultCh := make(chan error, 1)
	go func() {
		for _, s := range steps {
			start := time.Now()
			err := s.Run()
			holdSpinner(start)
			resultCh <- err
			if err != nil {
				return
			}
		}
	}()

	sm := newCardStepsModel(steps, successor, prefix, resultCh)
	p := tea.NewProgram(sm)
	model, err := p.Run()

	var stepErr error
	if err != nil {
		// Non-interactive fallback — drain the worker synchronously.
		for range steps {
			if stepErr = <-resultCh; stepErr != nil {
				break
			}
		}
		if stepErr != nil {
			fmt.Println(" " + stepErr.Error())
		} else if successor != nil {
			successor().Print()
		}
	} else {
		stepErr = model.(cardStepsModel).err
	}

	if stepErr != nil {
		return nil, stepErr
	}

	if successor == nil {
		needsSpacer = prevSpacer
		return func() {}, nil
	}

	// BubbleTea's final frame already painted the successor; compute
	// the rewind from the same render.
	rendered := successor()
	totalLines := strings.Count(prefix+rendered.Render(), "\n")
	return func() {
		if totalLines > 0 {
			fmt.Printf("\x1b[%dF\x1b[J", totalLines)
		}
		needsSpacer = prevSpacer
	}, nil
}

type cardStepsModel struct {
	steps     []CardStep
	successor func() *Card
	prefix    string
	idx       int
	done      bool
	err       error
	spinner   spinner.Model
	resultCh  <-chan error
}

type cardStepDoneMsg struct{ err error }

func newCardStepsModel(steps []CardStep, successor func() *Card, prefix string, resultCh <-chan error) cardStepsModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return cardStepsModel{
		steps: steps, successor: successor, prefix: prefix,
		spinner: s, resultCh: resultCh,
	}
}

func (m cardStepsModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.waitForStep())
}

func (m cardStepsModel) waitForStep() tea.Cmd {
	return func() tea.Msg {
		return cardStepDoneMsg{err: <-m.resultCh}
	}
}

func (m cardStepsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cardStepDoneMsg:
		if msg.err != nil {
			// Final frame: the failed step's card in failed state.
			m.err = msg.err
			m.steps[m.idx].Card.state = CardFailed
			m.steps[m.idx].Card.Subtitle(msg.err.Error())
			m.done = true
			return m, tea.Quit
		}
		m.idx++
		if m.idx >= len(m.steps) {
			m.done = true
			return m, tea.Quit
		}
		return m, m.waitForStep()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("interrupted")
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m cardStepsModel) View() tea.View {
	if m.done {
		if m.err != nil {
			return tea.NewView(m.prefix + m.steps[min(m.idx, len(m.steps)-1)].Card.Render())
		}
		if m.successor == nil {
			// Vanish: empty final frame clears the render area.
			return tea.NewView("")
		}
		return tea.NewView(m.prefix + m.successor().Render())
	}
	return tea.NewView(m.prefix + m.steps[m.idx].Card.renderWithGlyph(m.spinner.View()))
}
