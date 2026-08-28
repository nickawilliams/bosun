package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// syncBuffer is a mutex-guarded bytes.Buffer. Bubbletea writes to its
// output from two goroutines (the renderer's flush loop and the event
// loop's scrollback inserts); a TTY absorbs that at the syscall layer,
// but a bare bytes.Buffer is a data race.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestShellModelTailSwaps locks the live-tail contract: exactly one
// content kind owns the tail at a time, and a tail message replaces
// whatever was active.
func TestShellModelTailSwaps(t *testing.T) {
	m := newShellModel()

	next, _ := m.Update(sesTailMsg{text: "one\n"})
	m = next.(*shellModel)
	if got := m.View().Content; got != "one\n" {
		t.Fatalf("static tail = %q, want %q", got, "one\n")
	}

	next, _ = m.Update(sesSpinnerMsg{frame: "spin:" + GlyphSlot + "\n"})
	m = next.(*shellModel)
	if got := m.View().Content; !strings.HasPrefix(got, "spin:") || strings.Contains(got, GlyphSlot) {
		t.Fatalf("spinner tail = %q, want spin frame with the glyph substituted", got)
	}

	next, _ = m.Update(sesTailMsg{text: "two\n"})
	m = next.(*shellModel)
	if got := m.View().Content; got != "two\n" {
		t.Fatalf("tail after spinner resolve = %q, want %q", got, "two\n")
	}
	if m.spinning {
		t.Error("spinner still mounted after a tail swap")
	}
}

// TestShellModelInterruptOutsideForm locks the abandonment contract:
// ctrl+c with no form active ends the program with the interrupted
// error the legacy spinner primitives returned.
func TestShellModelInterruptOutsideForm(t *testing.T) {
	m := newShellModel()
	next, _ := m.Update(sesSpinnerMsg{frame: "spin\n"})
	m = next.(*shellModel)

	next, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = next.(*shellModel)
	if m.err == nil || m.err.Error() != "interrupted" {
		t.Fatalf("err = %v, want interrupted", m.err)
	}
	if !m.quitting {
		t.Error("model not quitting after ctrl+c")
	}
}

// TestShellModelFormMountAndStaleReply locks two form behaviors: a
// mounted form renders beneath its header in the view, and a
// form-done message for a block that is no longer mounted still
// replies (a worker must never block on a stale form).
func TestShellModelFormMountAndStaleReply(t *testing.T) {
	m := newShellModel()

	ok := true
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Value(&ok))).WithWidth(60)
	fb := &sesFormBlock{form: form, header: "HEADER\n", prefix: "", done: make(chan bool, 1)}

	next, _ := m.Update(sesFormMsg{fb: fb})
	m = next.(*shellModel)
	if got := m.View().Content; !strings.HasPrefix(got, "HEADER\n") {
		t.Fatalf("form view missing header: %q", got)
	}

	// Stale reply: a different block's done message must be answered
	// without disturbing the mounted form.
	stale := &sesFormBlock{done: make(chan bool, 1)}
	next, _ = m.Update(sesFormDoneMsg{fb: stale, aborted: true})
	m = next.(*shellModel)
	select {
	case aborted := <-stale.done:
		if !aborted {
			t.Error("stale reply lost the aborted flag")
		}
	default:
		t.Fatal("stale form-done message was not replied to")
	}
	if m.formTail != fb {
		t.Error("mounted form was disturbed by a stale done message")
	}

	// The real block's completion unmounts the form and keeps the
	// header visible until the worker posts the next tail.
	next, _ = m.Update(sesFormDoneMsg{fb: fb, aborted: false})
	m = next.(*shellModel)
	if m.formTail != nil {
		t.Error("form still mounted after completion")
	}
	if got := m.View().Content; got != "HEADER\n" {
		t.Errorf("view after submit = %q, want the bare header", got)
	}
	select {
	case <-fb.done:
	default:
		t.Fatal("form completion was not replied to")
	}
}

// TestShellModelGroupLifecycle locks the group path: children
// accumulate under the animated parent and the done message finalizes
// the aggregate state.
func TestShellModelGroupLifecycle(t *testing.T) {
	m := newShellModel()
	gb := &sesGroupBlock{gm: newGroupModel("work", 0, nil)}

	next, _ := m.Update(sesGroupStartMsg{gb: gb})
	m = next.(*shellModel)

	child := NewCard(CardSuccess, "child")
	child.Indent(1)
	next, _ = m.Update(sesGroupMsg{gb: gb, msg: groupChildMsg{rendered: child.Render(), state: CardSuccess}})
	m = next.(*shellModel)
	if got := m.View().Content; !strings.Contains(got, "Child") {
		t.Fatalf("group view missing child row: %q", got)
	}

	_, _ = m.Update(sesGroupMsg{gb: gb, msg: groupDoneMsg{}})
	if !gb.gm.root.finalized || gb.gm.root.finalState != CardSuccess {
		t.Errorf("group not finalized to success: finalized=%v state=%v",
			gb.gm.root.finalized, gb.gm.root.finalState)
	}
}

// TestRunSessionRawBypass locks the raw contract: with a non-card
// reporter installed, RunSession never starts a program — fn runs
// directly and the primitives take their existing raw branches.
func TestRunSessionRawBypass(t *testing.T) {
	old := Default()
	SetDefault(NewCaptureReporter())
	t.Cleanup(func() { SetDefault(old) })

	ran := false
	if err := RunSession(func() error { ran = true; return errors.New("boom") }); err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v, want boom", err)
	}
	if !ran {
		t.Error("fn did not run")
	}
	if InSession() {
		t.Error("session left active after raw bypass")
	}
}

// TestRunSessionCommitsInOrder is the end-to-end shell contract: a
// worker that prints a sequence of cards gets them committed to
// scrollback in call order (the Program.Println path), with the last
// block left as the program's final frame. Runs against buffer
// streams — the same injection surface the harness uses — so no PTY
// is involved.
func TestRunSessionCommitsInOrder(t *testing.T) {
	old := Default()
	SetDefault(NewCardReporter())
	var out syncBuffer
	SetStreams(strings.NewReader(""), &out, &out)
	t.Cleanup(func() {
		SetDefault(old)
		ResetStreams()
		ClearSpacer()
	})

	err := RunSession(func() error {
		NewCard(CardSuccess, "alpha").Print()
		NewCard(CardSuccess, "bravo").Print()
		rewind := NewCard(CardInput, "charlie").PrintRewindable()
		rewind()
		NewCard(CardSuccess, "delta").Print()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}

	text := out.String()
	ia, ib, id := strings.Index(text, "Alpha"), strings.Index(text, "Bravo"), strings.Index(text, "Delta")
	if ia < 0 || ib < 0 || id < 0 {
		t.Fatalf("missing committed cards: alpha=%d bravo=%d delta=%d in %q", ia, ib, id, text)
	}
	if ia >= ib || ib >= id {
		t.Errorf("commit order broken: alpha=%d bravo=%d delta=%d", ia, ib, id)
	}
	if InSession() {
		t.Error("session left active after RunSession returned")
	}
}

// TestRunSessionWorkerErrorPropagates locks the error contract: the
// worker's error is RunSession's return value.
func TestRunSessionWorkerErrorPropagates(t *testing.T) {
	old := Default()
	SetDefault(NewCardReporter())
	var out syncBuffer
	SetStreams(strings.NewReader(""), &out, &out)
	t.Cleanup(func() {
		SetDefault(old)
		ResetStreams()
		ClearSpacer()
	})

	want := errors.New("worker failed")
	start := time.Now()
	if err := RunSession(func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	// The teardown floor keeps the program alive long enough to
	// consume its own setup escapes, same as the legacy primitives.
	if elapsed := time.Since(start); elapsed < minSpinnerDuration {
		t.Errorf("session ended after %v, want >= %v", elapsed, minSpinnerDuration)
	}
}

// sessionTestStreams installs a card reporter and buffer streams so
// RunSession drives a real program headlessly, returning the output
// buffer and the input pipe writer (for form-driving tests).
func sessionTestStreams(t *testing.T) (*syncBuffer, *io.PipeWriter) {
	t.Helper()
	old := Default()
	SetDefault(NewCardReporter())
	pr, pw := io.Pipe()
	var out syncBuffer
	SetStreams(pr, &out, &out)
	t.Cleanup(func() {
		_ = pw.Close()
		SetDefault(old)
		ResetStreams()
		ClearSpacer()
		DiscardOpenCard()
	})
	return &out, pw
}

// TestRunSessionSpinnerAndSteps locks the animated-tail runners: the
// RunCard family and RunCardSteps resolve into committed cards in
// call order, failures resolve into failed cards carrying the error,
// and a morph rewind drops its block instead of committing it.
func TestRunSessionSpinnerAndSteps(t *testing.T) {
	out, _ := sessionTestStreams(t)

	err := RunSession(func() error {
		if err := RunCard("first task", func() error { return nil }); err != nil {
			return err
		}
		if err := RunCard("second task", func() error { return errors.New("kaput") }); err == nil {
			return errors.New("expected the second task's error")
		}
		rewind, err := RunCardMorph(NewCard(CardRunning, "morph header"), NewCard(CardInput, "morph header"), func() error { return nil })
		if err != nil {
			return err
		}
		rewind()
		steps := []CardStep{
			{Card: NewCard(CardRunning, "step one"), Run: func() error { return nil }},
			{Card: NewCard(CardRunning, "step two"), Run: func() error { return nil }},
		}
		if _, err := RunCardSteps(steps, func() *Card { return NewCard(CardSuccess, "steps done") }); err != nil {
			return err
		}
		NewCard(CardSuccess, "outro").Print()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}

	text := out.String()
	for _, want := range []string{"First Task", "Second Task", "kaput", "Steps Done", "Outro"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q", want)
		}
	}
	iFail, iSteps, iOutro := strings.Index(text, "kaput"), strings.Index(text, "Steps Done"), strings.Index(text, "Outro")
	if iFail < 0 || iSteps < 0 || iOutro < 0 || iFail > iSteps || iSteps > iOutro {
		t.Errorf("commit order broken: fail=%d steps=%d outro=%d", iFail, iSteps, iOutro)
	}
}

// TestRunSessionGroupCommits locks the group runner: children and the
// finalized parent land in the committed output.
func TestRunSessionGroupCommits(t *testing.T) {
	out, _ := sessionTestStreams(t)

	err := RunSession(func() error {
		RunGroup("gathering", func(g Reporter) {
			_ = g.Spinner(PreserveCase("repo-a"), func() error { return nil })
			g.Complete(PreserveCase("repo-a"))
			g.FailValue(PreserveCase("repo-b"), "broken")
		})
		NewCard(CardSuccess, "after group").Print()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
	text := out.String()
	for _, want := range []string{"Gathering", "repo-a", "repo-b", "broken", "After Group"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// TestRunSessionPlanApply locks the plan runner: the card transitions
// through Applying to its final state inside the session.
func TestRunSessionPlanApply(t *testing.T) {
	out, _ := sessionTestStreams(t)

	plan := NewPlan()
	plan.Add(PlanCreate, "deploy", "env", "test-env", "detail")
	pc := NewPlanCard(plan)
	err := RunSession(func() error {
		return pc.RunApply([]PlanAction{{Run: func() error { return nil }}})
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
	if !strings.Contains(out.String(), "test-env") {
		t.Errorf("output missing the plan row: %q", out.String())
	}
}

// TestRunSessionFormSubmitAndAbort locks the embedded-form path end
// to end: a confirm submitted with Enter resolves cleanly (the
// header rewinds, the record prints), and a second form aborted with
// ctrl+c returns huh.ErrUserAborted.
func TestRunSessionFormSubmitAndAbort(t *testing.T) {
	out, pw := sessionTestStreams(t)

	press := func(b byte) {
		time.Sleep(400 * time.Millisecond)
		_, _ = pw.Write([]byte{b})
	}

	err := RunSession(func() error {
		// Submit: header card + confirm, Enter accepts the default.
		rewind := NewCard(CardInput, "question one").Tight().PrintRewindable()
		ok := true
		f := buildTestForm(huh.NewConfirm().Value(&ok))
		go press('\r')
		if err := SessionForm(f, false); err != nil {
			return err
		}
		rewind()
		NewCard(CardSuccess, "answered").Print()

		// Abort: ctrl+c surfaces as ErrUserAborted.
		ok2 := true
		f2 := buildTestForm(huh.NewConfirm().Value(&ok2))
		go press(0x03)
		if err := SessionForm(f2, false); !errors.Is(err, huh.ErrUserAborted) {
			return errors.New("expected ErrUserAborted, got " + errString(err))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
	if !strings.Contains(out.String(), "Answered") {
		t.Errorf("output missing the post-submit record: %q", out.String())
	}
}

// TestRunSessionStepFailureAndVanish locks two resolution shapes: a
// failing step resolves the tail into the failed card (halting the
// sequence), and a vanishing morph leaves nothing behind.
func TestRunSessionStepFailureAndVanish(t *testing.T) {
	out, _ := sessionTestStreams(t)

	err := RunSession(func() error {
		steps := []CardStep{
			{Card: NewCard(CardRunning, "step ok"), Run: func() error { return nil }},
			{Card: NewCard(CardRunning, "step bad"), Run: func() error { return errors.New("step exploded") }},
			{Card: NewCard(CardRunning, "step never"), Run: func() error { return errors.New("must not run") }},
		}
		if _, err := RunCardSteps(steps, nil); err == nil || err.Error() != "step exploded" {
			return errors.New("expected the failing step's error")
		}
		// Vanish: nil final card, spinner area clears on success.
		rewind, err := RunCardMorph(NewCard(CardRunning, "ghost"), nil, func() error { return nil })
		if err != nil {
			return err
		}
		rewind()
		NewCard(CardSuccess, "survivor").Print()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "step exploded") {
		t.Errorf("output missing the failed step card: %q", text)
	}
	if !strings.Contains(text, "Survivor") {
		t.Errorf("output missing the post-vanish card: %q", text)
	}
}

// TestRunSessionInterruptAbandonsWorker locks the abandonment
// contract end to end: ctrl+c outside a form returns "interrupted"
// from RunSession without waiting for the worker, and the abandoned
// worker's subsequent primitive calls fail fast instead of blocking
// on the dead program.
func TestRunSessionInterruptAbandonsWorker(t *testing.T) {
	_, pw := sessionTestStreams(t)

	release := make(chan struct{})
	workerDone := make(chan error, 1)
	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = pw.Write([]byte{0x03}) // ctrl+c during the spinner
	}()

	start := time.Now()
	err := RunSession(func() error {
		err := RunCard("long task", func() error { <-release; return nil })
		workerDone <- err
		return err
	})
	if err == nil || err.Error() != "interrupted" {
		t.Fatalf("RunSession err = %v, want interrupted", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("RunSession waited %v for the abandoned worker", elapsed)
	}

	// Release the worker; its RunCard must fail fast on the closed
	// session rather than hanging.
	close(release)
	select {
	case werr := <-workerDone:
		if werr == nil || werr.Error() != "interrupted" {
			t.Errorf("abandoned worker err = %v, want interrupted", werr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abandoned worker blocked on the dead program")
	}
}

// TestRunSessionMultiFieldFormPrologue locks the multi-field form
// shape: the prologue row renders above the form and submission
// resolves cleanly.
func TestRunSessionMultiFieldFormPrologue(t *testing.T) {
	_, pw := sessionTestStreams(t)

	go func() {
		// Two fields: Enter through both.
		time.Sleep(400 * time.Millisecond)
		_, _ = pw.Write([]byte{'\r'})
		time.Sleep(200 * time.Millisecond)
		_, _ = pw.Write([]byte{'\r'})
	}()

	err := RunSession(func() error {
		a, b := true, true
		f := buildTestForm(huh.NewConfirm().Value(&a), huh.NewConfirm().Value(&b))
		return SessionForm(f, true)
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
}

// TestRunSessionRoutedHelpers sweeps the session branches of the
// smaller routed primitives — the free-line printers, the spacer
// helpers, the plan printers, the replace/rewindable spinner
// variants, and the steps-into fallback — locking that each commits
// or resolves without corrupting the tail.
func TestRunSessionRoutedHelpers(t *testing.T) {
	out, _ := sessionTestStreams(t)

	err := RunSession(func() error {
		Default().Muted("muted line")
		EmptyState("nothing here")
		SuccessLine("success line content")
		Bold("bold heading")
		FlushSpacer()
		FinalizeOpenCard()

		plan := NewPlan()
		plan.Add(PlanCreate, "make", "thing", "widget", "d")
		plan.Print()
		drop := plan.PrintRewindable()
		drop()
		pc := NewPlanCard(plan)
		pcDrop := pc.PrintRewindable()
		pcDrop()
		pc.Print()

		if err := RunCardReplace("replace me", func() error { return nil }, func() *Card {
			return NewCard(CardSuccess, "replaced")
		}); err != nil {
			return err
		}
		rewind, err := RunCardRewindable("rewindable", func() error { return nil })
		if err != nil {
			return err
		}
		rewind()

		// Steps-into fallback: final view becomes the open tail.
		if err := RunCardStepsInto([]CardStep{
			{Card: NewCard(CardRunning, "into step"), Run: func() error { return nil }},
		}, func() string { return NewCard(CardSuccess, "into final").Render() }); err != nil {
			return err
		}

		// Zero steps with a successor — the print-only path.
		r2, err := RunCardSteps(nil, func() *Card { return NewCard(CardSuccess, "empty steps") })
		if err != nil {
			return err
		}
		r2()
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}

	text := out.String()
	for _, want := range []string{"muted line", "nothing here", "success line content", "bold heading", "widget", "Replaced", "Into Final"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

// TestSessionFormOutsideSession locks the misuse guard: SessionForm
// with no active session errors instead of hanging.
func TestSessionFormOutsideSession(t *testing.T) {
	ok := true
	if err := SessionForm(buildTestForm(huh.NewConfirm().Value(&ok)), false); err == nil {
		t.Fatal("expected an error outside a session")
	}
}

// buildTestForm mirrors the cli layer's form construction closely
// enough for embedding tests: theme-free, fixed width.
func buildTestForm(fields ...huh.Field) *huh.Form {
	return huh.NewForm(huh.NewGroup(fields...)).WithWidth(60).WithShowHelp(false)
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
