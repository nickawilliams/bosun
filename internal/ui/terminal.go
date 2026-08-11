package ui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Stream sources for the UI layer. Default to the process streams.
// PersistentPreRunE replaces these via SetStreams using cobra's
// cmd.InOrStdin/OutOrStdout/ErrOrStderr accessors so tests that set
// cmd.SetIn/SetOut/SetErr flow through to runForm and rendering.
var (
	defaultInput  io.Reader = os.Stdin
	defaultOutput io.Writer = os.Stdout
	defaultErr    io.Writer = os.Stderr
)

// SetStreams replaces the package-level I/O streams. Nil arguments
// leave the corresponding stream unchanged.
func SetStreams(in io.Reader, out, errW io.Writer) {
	if in != nil {
		defaultInput = in
	}
	if out != nil {
		defaultOutput = out
	}
	if errW != nil {
		defaultErr = errW
	}
}

// ResetStreams restores the process streams. Tests use it via t.Cleanup
// so state doesn't leak between tests.
func ResetStreams() {
	defaultInput = os.Stdin
	defaultOutput = os.Stdout
	defaultErr = os.Stderr
}

// InputHandoff is implemented by injected input readers (the test
// harness) that need to know when the stream's consumer changes.
// bubbletea can't cancel reads on a non-File reader, so a finished
// form's leaked read loop would otherwise steal — and discard —
// input meant for the next form in the same command run. Input()
// announces acquisition (a new form taking the stream) and
// ReleaseInput() announces exit (the form is done), so the reader
// can refuse delivery to reads begun under an earlier session.
type InputHandoff interface {
	NextConsumer()
}

// Input returns the current input stream, announcing a consumer
// handoff to readers that track sessions (see InputHandoff). Called
// once per form construction, which is exactly the consumer boundary.
func Input() io.Reader {
	if h, ok := defaultInput.(InputHandoff); ok {
		h.NextConsumer()
	}
	return defaultInput
}

// ReleaseInput announces that the current form's consumer is done
// with the input stream (see InputHandoff). Bumping the session at
// form EXIT — not only at the next form's start — invalidates the
// exited form's leaked read loop structurally: its in-flight read
// began under the old session regardless of how much time passes
// before the next prompt, so it can never consume that prompt's keys.
func ReleaseInput() {
	if h, ok := defaultInput.(InputHandoff); ok {
		h.NextConsumer()
	}
}

// Output returns the current output stream.
func Output() io.Writer { return defaultOutput }

// ErrOutput returns the current error stream.
func ErrOutput() io.Writer { return defaultErr }

// Interactive reports whether the input stream supports prompting.
// Non-*os.File readers (test buffers) are treated as interactive so
// injected input drives prompts; real os.Stdin must be a TTY.
//
// The non-File branch exists to enable test injection (a bytes.Buffer
// stand-in for stdin counts as interactive). It is not a security
// or correctness guarantee — a misconfigured caller that wraps real
// os.Stdin in bufio.Reader or similar would also report interactive.
// In this codebase the only producer of non-File readers is the test
// harness; if that ever changes, this heuristic needs revisiting.
func Interactive() bool {
	f, ok := defaultInput.(*os.File)
	if !ok {
		return true
	}
	return term.IsTerminal(int(f.Fd()))
}

// TermWidth returns the current output terminal width, defaulting to 80
// when the output isn't a *os.File or the syscall fails.
func TermWidth() int {
	f, ok := defaultOutput.(*os.File)
	if !ok {
		return 80
	}
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}

// IsTerminal reports whether the current output stream is a TTY.
func IsTerminal() bool { return IsTerminalWriter(defaultOutput) }

// IsTerminalWriter reports whether w is a TTY-backed file. Returns
// false for buffers, pipes, and other non-*os.File writers.
func IsTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// CanRenderInteractively reports whether the current output stream
// can present an interactive UI to a human. True when output is a
// real TTY (production interactive use) or any non-*os.File writer
// (test harness buffers). False when output is a *os.File but not a
// terminal — i.e. piped stdout in production, where drawing a huh
// form would emit ANSI to the pipe while still blocking the user
// on stdin with no visible prompt.
func CanRenderInteractively() bool {
	if f, ok := defaultOutput.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return true
}
