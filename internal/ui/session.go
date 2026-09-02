package ui

// The single-program command shell (issue #31).
//
// # Model contract
//
// A command that runs inside RunSession gets ONE BubbleTea program for
// its entire lifetime. Every UI primitive (Card.Print, the RunCard*
// spinners, RunGroup, PlanCard.RunApply, huh forms via the cli layer)
// detects the active session and routes through it as messages instead
// of printing directly or launching its own tea.Program. Phase
// transitions are therefore atomic frame swaps inside one renderer —
// the blank flash at every program boundary is gone by construction.
//
// The contract has four load-bearing rules:
//
//  1. **Live tail only in View.** The program's View renders only the
//     current tail: the last finalized block (in OPEN form — no spine
//     on its body lines) plus whatever is active beneath it (an
//     animating spinner, an embedded huh form, a group in flight).
//     Bubbletea's inline renderer clips frames taller than the
//     terminal from the top and managed content never enters
//     scrollback, so the timeline must not accumulate in the view.
//
//  2. **Finalized content commits to scrollback via Program.Println.**
//     When a new block starts, the previous open block is committed in
//     CONTINUING form (spine restored). Committed content is
//     immutable: anything that needs to change after being shown — a
//     rewindable prompt header, a morphing spinner card — stays in the
//     live tail until it resolves, then commits. Rewind is therefore a
//     view-drop, not an ANSI cursor-up erase.
//
//  3. **The worker owns ordering — and all rendering.** The command
//     body runs on a worker goroutine and is the single writer: it
//     computes every rendered string (snapshotting wrap widths at
//     emit time, like the openCardRecord mechanic), performs commits
//     via Program.Println, and posts tail swaps via Program.Send.
//     Both enter the same program message queue, so scrollback order
//     is the call order. The model goroutine holds only immutable
//     strings — animated frames carry GlyphSlot placeholders it
//     substitutes with the live spinner glyph — so worker-side
//     mutation of cards between frames can never race the renderer.
//
//  4. **Forms are embedded, not subprocesses.** huh.Form implements
//     the tea model contract; the session hosts the form in the tail
//     beneath its (uncommitted) header card, overrides SubmitCmd /
//     CancelCmd with session messages, and forwards input to it.
//     Submit resolves the tail; abort freezes the form's last frame
//     into the tail so the caller's cancellation card reads in
//     context. There is no program handoff and no cursor takeover.
//
// Raw / non-TTY / test-capture modes bypass the session entirely
// (RunSession just calls fn), so every primitive's existing raw branch
// — and the CaptureReporter test contract built on it — is unchanged.
//
// Interrupts: ctrl+c during a form is the form's abort (ErrCancelled
// path, worker unwinds normally). ctrl+c as a key press anywhere else
// ends the program, as does an external SIGINT (which bubbletea turns
// into ErrInterrupted from Run without the model ever seeing it).
// Either way RunSession returns "interrupted" immediately and the
// worker goroutine is abandoned, matching the legacy primitives'
// behavior of returning from a spinner while its task goroutine still
// runs.
//
// Abandonment is deliberately best-effort, and the process is expected
// to exit shortly after. A call the worker has already entered
// unwinds via session.interrupted; a call it makes AFTER abandonment
// finds no active session and falls through to the legacy
// non-session path, exactly as it would have before this shell
// existed. That can briefly interleave with the error card the main
// goroutine renders — bounded by process exit, and preferable to
// swallowing output on a path where the user is already leaving.
//
// One bubbletea constraint shapes the failure paths: Program.Println
// is an unguarded send on an unbuffered channel (unlike Program.Send,
// which selects on the program context), so a commit racing program
// teardown can park forever. Commits therefore go through Println for
// its FIFO ordering guarantee — routing them as commands instead
// would let two scrollback inserts land out of order — and every path
// that waits on the worker is bounded so a parked commit can never
// hang the command.

import (
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// errInterrupted matches the error text the legacy spinner primitives
// return when ctrl+c ends a run mid-task.
var errInterrupted = errors.New("interrupted")

// sessionDrainTimeout bounds how long a failed run waits for the
// worker's result. The worker can be parked forever in a commit whose
// program has gone away (Program.Println is an unguarded send), and a
// command that hangs with no UI is strictly worse than one that
// reports the renderer error it already has in hand.
const sessionDrainTimeout = 2 * time.Second

// session is the worker-side handle for the active shell program. All
// fields except closed are owned by the worker goroutine; main touches
// them only before the worker starts and after it finishes (or is
// abandoned, in which case only the atomic closed flag is written).
type session struct {
	prog    *tea.Program
	started time.Time

	// open is the tail's finalized block: rendered once in each form
	// at emit time. nil when the tail is empty or active content owns
	// the whole tail.
	open *sesOpenRec

	// closed marks the program as gone (interrupt abandonment or run
	// failure). Worker-side session calls check it so an in-flight
	// primitive unwinds instead of pushing onto a dead program's
	// queue; calls made after abandonment see no active session at
	// all and take the legacy path (see the package header).
	closed atomic.Bool

	// done closes when the program has exited, releasing any worker
	// call still blocked on a reply (an embedded form, chiefly).
	done chan struct{}
}

// sesOpenRec snapshots a finalized block in both timeline forms, the
// session-mode analog of openCardRecord. Strings include the block's
// spacer prefix so open and continuing always have equal height.
type sesOpenRec struct {
	open       string
	continuing string
	prevSpacer bool // needsSpacer before this block printed; restored on rewind
}

// activeSession holds the running session. Written by RunSession
// before the worker starts and after it joins; read from the worker.
// The only concurrent access is the abandoned-worker case, which is
// gated by the atomic closed flag.
var activeSession atomic.Pointer[session]

// InSession reports whether a shell session is currently driving the
// terminal. Ported call sites use it to pick session-only flows.
func InSession() bool { return sessionActive() != nil }

// sessionActive returns the live session, or nil when none is running
// or the program is gone.
func sessionActive() *session {
	s := activeSession.Load()
	if s == nil || s.closed.Load() {
		return nil
	}
	return s
}

// RunSession runs fn — a command body — under the single-program
// shell. In raw mode (plain, machine-readable, or test capture) or
// when the output cannot host an interactive renderer, fn runs
// directly and every primitive takes its existing non-session path.
// Nested calls run fn directly under the already-active session.
func RunSession(fn func() error) error {
	if IsRaw() || !CanRenderInteractively() || activeSession.Load() != nil {
		return fn()
	}

	// A card printed before the session started can't be reached once
	// the program owns the screen; give it its spine now.
	FinalizeOpenCard()

	m := newShellModel()
	opts := []tea.ProgramOption{tea.WithInput(defaultInput), tea.WithOutput(defaultOutput), TeaColorProfile()}
	if !IsTerminalWriter(defaultOutput) {
		// A non-TTY output (injected buffers) can't answer the size
		// query, and a zero-size renderer paints nothing. Real TTYs
		// keep their detected size.
		opts = append(opts, tea.WithWindowSize(TermWidth(), TermHeight()))
	}
	p := tea.NewProgram(m, opts...)

	s := &session{prog: p, started: time.Now(), done: make(chan struct{})}
	activeSession.Store(s)
	defer activeSession.Store(nil)

	workerErr := make(chan error, 1)
	var started atomic.Bool
	m.onStart = func() {
		started.Store(true)
		go func() {
			err := fn()
			// Keep the program alive long enough to consume its own
			// terminal-capability query responses (same floor the
			// per-primitive programs used — see minSpinnerDuration).
			holdSpinner(s.started)
			workerErr <- err
			if !s.closed.Load() {
				p.Send(sesQuitMsg{})
			}
		}()
	}

	final, runErr := p.Run()
	close(s.done)

	// Run can fail before Init — OpenTTY, terminal setup, the size
	// query, and the input reader all precede it — in which case
	// onStart never fired and no worker exists to wait for. Nothing
	// has painted either, so run the body directly on the legacy
	// primitives: the same fallback the per-primitive programs had.
	if !started.Load() {
		s.closed.Store(true)
		return fn()
	}

	if runErr != nil {
		// Either an external SIGINT (bubbletea returns ErrInterrupted
		// from Run rather than routing it through the model) or a
		// renderer failure. Abandon in both cases; the worker may be
		// parked in a commit whose program is gone, so the wait is
		// bounded rather than open-ended.
		s.closed.Store(true)
		if errors.Is(runErr, tea.ErrInterrupted) {
			return errInterrupted
		}
		select {
		case err := <-workerErr:
			return err
		case <-time.After(sessionDrainTimeout):
			return runErr
		}
	}

	if fm, ok := final.(*shellModel); ok && fm.err != nil {
		// ctrl+c as a key press: abandon the worker, mirroring the
		// legacy primitives (their programs returned "interrupted"
		// while the task goroutine kept running until process exit).
		s.closed.Store(true)
		return fm.err
	}

	err := <-workerErr

	// Post-session prints (HandleError cards, EndTimeline) happen
	// below the program's final frame; hand the tail block to the
	// legacy timeline mechanic so the next direct print restores its
	// spine.
	if s.open != nil {
		recordOpenCard(s.open.open, s.open.continuing)
	}
	return err
}

// --- worker-side emission API (used by the routed primitives) ---

// sessionPrefix mirrors spacerPrefix for session blocks: the comfy
// connector row when one is pending, arming the flag for next time.
// It does NOT close the open card — session commits handle that.
func sessionPrefix() string {
	if !needsSpacer {
		needsSpacer = true
		return ""
	}
	conn := lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
	return " " + conn + "\n"
}

// send posts a message to the program unless the session is closed.
func (s *session) send(msg tea.Msg) {
	if s.closed.Load() {
		return
	}
	s.prog.Send(msg)
}

// commitOpen commits the current open block to scrollback in
// continuing form and clears it. The Println goes through the
// program's message queue, so it is ordered with subsequent sends.
func (s *session) commitOpen() {
	if s.open == nil {
		return
	}
	s.println(s.open.continuing)
	s.open = nil
	// Drop the committed block from the managed frame too, or it
	// stays painted below its own scrollback copy. Callers that mount
	// replacement content post their own tail immediately after, and
	// both messages travel the same FIFO queue, so the intermediate
	// clear never reaches the screen on those paths.
	s.send(sesTailMsg{text: ""})
}

// println commits raw text to scrollback. Text carries its own
// trailing newline (every block render does); Program.Println adds
// one, so it is trimmed here.
func (s *session) println(text string) {
	if s.closed.Load() || text == "" {
		return
	}
	s.prog.Println(strings.TrimSuffix(text, "\n"))
}

// print makes a finalized block the tail: commits the previous open
// block and shows this one in open form. tight suppresses the comfy
// spacer after this block (Card.Tight semantics).
func (s *session) print(open, continuing string, tight bool) *sesOpenRec {
	prevSpacer := needsSpacer
	prefix := sessionPrefix()
	s.commitOpen()
	rec := &sesOpenRec{open: prefix + open, continuing: prefix + continuing, prevSpacer: prevSpacer}
	s.open = rec
	s.send(sesTailMsg{text: rec.open})
	if tight {
		ClearSpacer()
	}
	return rec
}

// drop rewinds a block: if rec is still the tail, it vanishes from
// the view (it was never committed). The session-mode analog of the
// PrintRewindable cursor-up erase.
func (s *session) drop(rec *sesOpenRec) {
	if rec == nil || s.open != rec {
		return
	}
	s.open = nil
	s.send(sesTailMsg{text: ""})
	needsSpacer = rec.prevSpacer
}

// spinnerStart commits the open block and mounts an animated tail.
// frame is a snapshot rendered worker-side with GlyphSlot standing in
// for the spinner glyph; the model substitutes the live frame on each
// tick. Passing a string rather than a render closure is load-bearing:
// the model never reads worker-owned state, so a worker mutating its
// card between frames (the failure finalizer, step accumulation) can
// never race the render loop.
func (s *session) spinnerStart(frame string) {
	s.commitOpen()
	s.send(sesSpinnerMsg{frame: frame})
}

// spinnerSwap replaces the animated tail's frame without a commit —
// the step-sequence mechanic: successive cards occupy one position.
func (s *session) spinnerSwap(frame string) {
	s.send(sesSpinnerMsg{frame: frame})
}

// spinnerFinish resolves the animated tail into a finalized block (or
// nothing, when vanish). Returns the record for rewinds.
func (s *session) spinnerFinish(open, continuing string, vanish bool) *sesOpenRec {
	// A card printed from inside the spinner's task became the open
	// block; commit it rather than overwriting it. Emitting during a
	// spinner is against the house style (see cleanup.go) and no
	// caller does it today, but losing the output entirely is a worse
	// failure than the legacy path's garbled-but-present one. A no-op
	// on every well-behaved path, where the open block is already nil.
	s.commitOpen()
	if vanish {
		s.open = nil
		s.send(sesTailMsg{text: ""})
		return nil
	}
	rec := &sesOpenRec{open: open, continuing: continuing}
	s.open = rec
	s.send(sesTailMsg{text: rec.open})
	return rec
}

// interrupted reports whether the program has gone away (ctrl+c
// abandonment); routed primitives return errInterrupted so the
// command unwinds.
func (s *session) interrupted() bool { return s.closed.Load() }

// --- forms ---

// SessionForm embeds a huh form in the active session's tail, beneath
// the current open block (typically the prompt's header card), and
// blocks until the user submits or aborts. multiField selects the
// prologue shape (the recessed │ row emitFormPrologue draws above
// multi-field forms). Returns huh.ErrUserAborted on cancel; the cli
// layer maps that to ErrCancelled.
//
// On submit the form vanishes from the tail (the header stays, open,
// for the caller to rewind or build on). On abort the form's last
// frame freezes into the tail so the cancellation card that follows
// reads as a response to the question still on screen.
func SessionForm(f *huh.Form, multiField bool) error {
	s := sessionActive()
	if s == nil {
		return errors.New("ui: SessionForm called with no active session")
	}

	var prefix string
	if multiField {
		conn := lipgloss.NewStyle().Foreground(Palette.Recessed).Render(BoxVertical)
		ClearSpacer()
		prefix = " " + conn + "\n"
		RequestSpacer()
	} else {
		prefix = sessionPrefix()
	}

	fb := &sesFormBlock{form: f, prefix: prefix, done: make(chan bool, 1)}
	if s.open != nil {
		// The header renders in continuing form while the form sits
		// beneath it; equal open/continuing heights make the swap
		// invisible when the form resolves.
		fb.header = s.open.continuing
	}
	f.SubmitCmd = func() tea.Msg { return sesFormDoneMsg{fb: fb, aborted: false} }
	f.CancelCmd = func() tea.Msg { return sesFormDoneMsg{fb: fb, aborted: true} }

	s.send(sesFormMsg{fb: fb})

	select {
	case aborted := <-fb.done:
		if aborted {
			// Freeze the abandoned question into the tail. The done
			// send happens after the model's last touch of the form,
			// so reading its view here is race-free.
			frozen := fb.header + fb.prefix + ensureTrailingNL(f.View())
			s.open = &sesOpenRec{open: frozen, continuing: frozen}
			s.send(sesTailMsg{text: frozen})
			return huh.ErrUserAborted
		}
		// Form cleared; header returns to being the open tail.
		if s.open != nil {
			s.send(sesTailMsg{text: s.open.open})
		} else {
			s.send(sesTailMsg{text: ""})
		}
		return nil
	case <-s.done:
		return huh.ErrUserAborted
	}
}

// ensureTrailingNL guarantees block strings end in a newline so tail
// composition stays line-aligned.
func ensureTrailingNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// --- groups ---

// runSessionGroup hosts a RunGroup inside the session: the same group
// reporter and message vocabulary, animated by the shell's model. The
// worker keeps a mirror groupModel fed by the identical message
// stream, so it can compute the final static render (the strings all
// originate worker-side, making the two instances deterministic
// twins).
func (s *session) runSessionGroup(title string, fn func(g Reporter)) {
	prefix := sessionPrefix()
	s.commitOpen()

	mirror := newGroupModel(title, 0, nil)
	gb := &sesGroupBlock{gm: newGroupModel(title, 0, nil), prefix: prefix}
	s.send(sesGroupStartMsg{gb: gb})

	msgCh := make(chan groupMsg, 256)
	drained := make(chan struct{})
	go func() {
		for msg := range msgCh {
			mirror.processMsg(msg)
			s.send(sesGroupMsg{gb: gb, msg: msg})
			if _, ok := msg.(groupDoneMsg); ok {
				break
			}
		}
		close(drained)
	}()

	g := &group{outer: defaultReporter, title: title, indent: 1, msgCh: msgCh}
	start := time.Now()
	fn(g)
	holdSpinner(start) // display floor, as cardReporter.Group applies
	msgCh <- groupDoneMsg{}
	<-drained

	final := prefix + mirror.viewString()
	rec := &sesOpenRec{open: final, continuing: final}
	s.open = rec
	s.send(sesTailMsg{text: final})
}

// --- messages ---

type sesTailMsg struct{ text string }

// sesSpinnerMsg carries a pre-rendered frame with GlyphSlot marking
// every position the animated spinner glyph should occupy.
type sesSpinnerMsg struct{ frame string }

type sesFormBlock struct {
	form   *huh.Form
	header string // open block rendered in continuing form (or "")
	prefix string // prologue rows above the form
	done   chan bool
}

type sesFormMsg struct{ fb *sesFormBlock }

type sesFormDoneMsg struct {
	fb      *sesFormBlock
	aborted bool
}

type sesGroupBlock struct {
	gm     *groupModel
	prefix string
}

type sesGroupStartMsg struct{ gb *sesGroupBlock }

type sesGroupMsg struct {
	gb  *sesGroupBlock
	msg groupMsg
}

type sesQuitMsg struct{}

// --- model ---

// shellModel is the session's tea model: a dumb live-tail renderer.
// All rendered content arrives as strings or render closures from the
// worker; the model contributes only spinner animation and form
// message routing.
type shellModel struct {
	spin     spinner.Model
	tail     string         // static tail content
	spinTail string         // animated tail frame (GlyphSlot placeholders), "" when static
	spinning bool           // spinTail is mounted (distinguishes an empty frame)
	formTail *sesFormBlock  // embedded form, nil when none
	group    *sesGroupBlock // animated group, nil when none
	onStart  func()
	err      error
	quitting bool
}

func newShellModel() *shellModel {
	s := spinner.New(
		spinner.WithSpinner(spinner.MiniDot),
		spinner.WithStyle(lipgloss.NewStyle().Foreground(Palette.Primary)),
	)
	return &shellModel{spin: s}
}

func (m *shellModel) Init() tea.Cmd {
	start := func() tea.Msg {
		if m.onStart != nil {
			m.onStart()
		}
		return nil
	}
	return tea.Batch(m.spin.Tick, start)
}

// clearActive resets every active-tail slot; exactly one content kind
// owns the tail at a time.
func (m *shellModel) clearActive() {
	m.spinTail = ""
	m.spinning = false
	m.formTail = nil
	m.group = nil
}

func (m *shellModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sesTailMsg:
		m.clearActive()
		m.tail = msg.text
		return m, nil

	case sesSpinnerMsg:
		m.clearActive()
		m.tail = ""
		m.spinTail = msg.frame
		m.spinning = true
		return m, nil

	case sesFormMsg:
		m.clearActive()
		m.tail = ""
		m.formTail = msg.fb
		return m, msg.fb.form.Init()

	case sesGroupStartMsg:
		m.clearActive()
		m.tail = ""
		m.group = msg.gb
		return m, nil

	case sesGroupMsg:
		if m.group == msg.gb {
			m.group.gm.processMsg(msg.msg)
		}
		return m, nil

	case sesFormDoneMsg:
		if m.formTail != msg.fb {
			msg.fb.done <- msg.aborted
			return m, nil
		}
		m.formTail = nil
		// Keep the header visible until the worker posts the next
		// tail; prevents a one-frame hole between submit and the
		// worker's follow-up.
		m.tail = msg.fb.header
		msg.fb.done <- msg.aborted
		return m, nil

	case sesQuitMsg:
		m.quitting = true
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if m.formTail != nil {
			return m.updateForm(msg)
		}
		if msg.String() == "ctrl+c" {
			m.err = errInterrupted
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

		// No InterruptMsg case: bubbletea's event loop returns
		// ErrInterrupted from Run before the message reaches Update,
		// so an external SIGINT is handled in RunSession instead.
	}

	if m.formTail != nil {
		return m.updateForm(msg)
	}
	return m, nil
}

// updateForm forwards a message to the embedded huh form.
func (m *shellModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	fm, cmd := m.formTail.form.Update(msg)
	if f, ok := fm.(*huh.Form); ok {
		m.formTail.form = f
	}
	return m, cmd
}

func (m *shellModel) View() tea.View {
	var b strings.Builder
	switch {
	case m.spinning:
		b.WriteString(strings.ReplaceAll(m.spinTail, GlyphSlot, m.spin.View()))
	case m.formTail != nil:
		b.WriteString(m.formTail.header)
		b.WriteString(m.formTail.prefix)
		b.WriteString(ensureTrailingNL(m.formTail.form.View()))
	case m.group != nil:
		m.group.gm.spinner = m.spin
		b.WriteString(m.group.prefix)
		b.WriteString(m.group.gm.viewString())
	default:
		b.WriteString(m.tail)
	}
	return tea.NewView(b.String())
}

// --- session paths for spinner-driven primitives ---

// sessionRunCard is the session path shared by the RunCard family:
// animate card while fn runs, then resolve to the finalized card (or
// successCard on success, or nothing when vanish). Returns the rewind
// record (nil on failure or vanish) and fn's error.
func sessionRunCard(s *session, card *Card, fn func() error, successCard func() *Card) (*sesOpenRec, error) {
	prevSpacer := needsSpacer
	prefix := sessionPrefix()
	card.state = CardRunning
	s.spinnerStart(prefix + card.renderWithGlyph(GlyphSlot))

	start := time.Now()
	err := fn()
	// Same display floor the legacy per-primitive runners applied, so
	// a fast task's spinner is still readable rather than a blip.
	holdSpinner(start)
	if s.interrupted() {
		if err == nil {
			err = errInterrupted
		}
		return nil, err
	}

	if err != nil {
		card.state = CardFailed
		card.Subtitle(err.Error())
		s.spinnerFinish(prefix+card.Render(), prefix+card.renderContinuing(), false)
		return nil, err
	}
	final := card
	final.state = CardSuccess
	if successCard != nil {
		final = successCard()
	}
	rec := s.spinnerFinish(prefix+final.Render(), prefix+final.renderContinuing(), false)
	if rec != nil {
		rec.prevSpacer = prevSpacer
	}
	// Tight suppresses the connector before whatever renders next —
	// the resolution paints through the program rather than Print, so
	// it has to be applied here (Card.Tight's contract, and what keeps
	// an embedded form flush against its header).
	if final.tight {
		ClearSpacer()
	}
	return rec, nil
}

// sessionRunSteps drives a step sequence on the animated tail:
// successive cards swap in place with no boundary between them.
// Returns the first step error (with the failed card resolved into
// the tail) or errInterrupted on abandonment.
func sessionRunSteps(s *session, prefix string, steps []CardStep) error {
	for i := range steps {
		steps[i].Card.state = CardRunning
	}
	for i := range steps {
		card := steps[i].Card
		s.spinnerSwap(prefix + card.renderWithGlyph(GlyphSlot))
		start := time.Now()
		err := steps[i].Run()
		holdSpinner(start)
		if s.interrupted() {
			if err == nil {
				err = errInterrupted
			}
			return err
		}
		if err != nil {
			card.state = CardFailed
			card.Subtitle(err.Error())
			s.spinnerFinish(prefix+card.Render(), prefix+card.renderContinuing(), false)
			return err
		}
	}
	return nil
}

// sessionRunCardSteps is RunCardSteps' session path: steps swap in
// one tail position, the successor resolves the tail, and the rewind
// is a view drop.
func sessionRunCardSteps(s *session, steps []CardStep, successor func() *Card) (func(), error) {
	prevSpacer := needsSpacer
	prefix := sessionPrefix()
	s.commitOpen()

	if len(steps) == 0 && successor == nil {
		needsSpacer = prevSpacer
		return func() {}, nil
	}

	if err := sessionRunSteps(s, prefix, steps); err != nil {
		return nil, err
	}

	if successor == nil {
		s.spinnerFinish("", "", true)
		needsSpacer = prevSpacer
		return func() {}, nil
	}
	final := successor()
	rec := s.spinnerFinish(prefix+final.Render(), prefix+final.renderContinuing(), false)
	if rec != nil {
		rec.prevSpacer = prevSpacer
	}
	// See sessionRunCard: the gather seams end on a Tight input header
	// that an embedded form renders directly beneath.
	if final.tight {
		ClearSpacer()
	}
	return s.sessionRewind(rec), nil
}

// sessionRewind wraps a record drop in the no-op-when-nil closure
// shape the rewindable primitives return.
func (s *session) sessionRewind(rec *sesOpenRec) func() {
	return func() {
		if s.interrupted() {
			return
		}
		s.drop(rec)
	}
}
