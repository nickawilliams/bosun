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
// derive the final card from state the steps accumulated. It MUST be
// pure with respect to repeated calls: the program's final frame and
// the rewind's line count come from separate evaluations, so a
// successor whose render differs between calls erases the wrong
// number of lines. A nil successor vanishes: empty final frame,
// no-op rewind. On success the returned rewind erases the successor
// block (same contract as RunCardMorph).
func RunCardSteps(steps []CardStep, successor func() *Card) (func(), error) {
	if IsRaw() {
		for _, s := range steps {
			if err := s.Run(); err != nil {
				return nil, err
			}
		}
		// Raw output gets the successor too — piped/CI runs would
		// otherwise lose the final result card entirely (interactive
		// runs paint it as the program's last frame). Input-state
		// successors are form headers for prompts raw mode never
		// runs, so those stay silent.
		if successor != nil {
			if final := successor(); final != nil && final.state != CardInput {
				final.Print()
			}
		}
		return func() {}, nil
	}

	prevSpacer := needsSpacer
	prefix := spacerPrefix()

	// No steps — nothing to animate; just print the successor (or
	// nothing) and hand back its rewind. needsSpacer stays armed while
	// the successor is on screen (matching the stepped path) and only
	// the rewind restores it — resetting before the print left the
	// next card without its comfy connector whenever prevSpacer was
	// false.
	if len(steps) == 0 {
		if successor == nil {
			needsSpacer = prevSpacer
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

	resultCh, quit := startStepWorker(steps)

	sm := newCardStepsModel(steps, successor, prefix, resultCh)
	p := tea.NewProgram(sm)
	model, err := p.Run()

	var stepErr error
	if err != nil {
		// Non-interactive fallback — drain the worker synchronously.
		// A failure renders the failed step's card (same shape the
		// interactive path paints) rather than a bare error line.
		for i := range steps {
			if stepErr = <-resultCh; stepErr != nil {
				steps[i].Card.state = CardFailed
				steps[i].Card.Subtitle(stepErr.Error())
				steps[i].Card.Print()
				break
			}
		}
		if stepErr == nil && successor != nil {
			successor().Print()
		}
	} else {
		stepErr = model.(cardStepsModel).err
	}
	close(quit)

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

// RunCardStepsInto is RunCardSteps with a raw final frame: on success
// the program's last frame is finalView()'s string verbatim (no Card
// wrapping), letting callers hand the terminal off seamlessly to a
// follow-up program — paint that program's exact first frame here,
// cursor-up over it, and its first repaint is byte-identical (the
// RunCardMorph no-flash principle, extended across program kinds).
// No rewind is returned; the caller owns the region from here.
func RunCardStepsInto(steps []CardStep, finalView func() string) error {
	if IsRaw() {
		for _, s := range steps {
			if err := s.Run(); err != nil {
				return err
			}
		}
		return nil
	}

	prefix := spacerPrefix()

	// No steps — nothing to animate (and the model's View would index
	// an empty slice). Leave the final frame on screen exactly as a
	// stepped run would: the caller owns the region from here.
	if len(steps) == 0 {
		fmt.Print(prefix + finalView())
		return nil
	}

	for _, s := range steps {
		s.Card.state = CardRunning
	}

	resultCh, quit := startStepWorker(steps)

	sm := newCardStepsModel(steps, nil, prefix, resultCh)
	sm.rawSuccessor = finalView
	p := tea.NewProgram(sm)
	model, err := p.Run()
	if err != nil {
		// Non-interactive fallback — drain the worker synchronously.
		var stepErr error
		for range steps {
			if stepErr = <-resultCh; stepErr != nil {
				break
			}
		}
		close(quit)
		if stepErr != nil {
			fmt.Println(" " + stepErr.Error())
			return stepErr
		}
		fmt.Print(prefix + finalView())
		return nil
	}
	close(quit)
	return model.(cardStepsModel).err
}

// startStepWorker executes steps sequentially on a goroutine,
// reporting each completion on the returned channel. holdSpinner
// keeps every step's frame on screen long enough to read instead of
// strobing through fast items. Closing quit stops the worker between
// steps — the in-flight step still finishes (steps carry no context
// to cancel), but no further step's side effects run — and unblocks
// a pending send so an interrupted run never leaks the goroutine.
func startStepWorker(steps []CardStep) (resultCh chan error, quit chan struct{}) {
	resultCh = make(chan error, 1)
	quit = make(chan struct{})
	go func() {
		for _, s := range steps {
			select {
			case <-quit:
				return
			default:
			}
			start := time.Now()
			err := s.Run()
			holdSpinner(start)
			select {
			case resultCh <- err:
			case <-quit:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return resultCh, quit
}

type cardStepsModel struct {
	steps        []CardStep
	successor    func() *Card
	rawSuccessor func() string
	prefix       string
	idx          int
	done         bool
	err          error
	spinner      spinner.Model
	resultCh     <-chan error
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
			// Flip the in-flight card out of CardRunning so the final
			// frame doesn't show a forever-"running" glyph above the
			// cancellation output.
			if m.idx < len(m.steps) {
				m.steps[m.idx].Card.state = CardFailed
				m.steps[m.idx].Card.Subtitle("interrupted")
			}
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
		if m.rawSuccessor != nil {
			// Raw final frame (RunCardStepsInto): the caller's string
			// verbatim — typically the next program's exact first
			// frame for a seamless takeover.
			return tea.NewView(m.prefix + m.rawSuccessor())
		}
		if m.successor == nil {
			// Vanish: empty final frame clears the render area.
			return tea.NewView("")
		}
		return tea.NewView(m.prefix + m.successor().Render())
	}
	return tea.NewView(m.prefix + m.steps[m.idx].Card.renderWithGlyph(m.spinner.View()))
}
