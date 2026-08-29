package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nickawilliams/bosun/internal/ui"
)

// syncWriter is a mutex-guarded buffer: bubbletea writes to its output
// from both the renderer loop and the event loop, which a TTY absorbs
// at the syscall layer but a bare bytes.Buffer does not.
type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// sessionFormStreams installs a card reporter with pipe-backed streams
// so runForm takes its session path against a real program.
func sessionFormStreams(t *testing.T) (*syncWriter, *io.PipeWriter) {
	t.Helper()
	old := ui.Default()
	ui.SetDefault(ui.NewCardReporter())
	pr, pw := io.Pipe()
	var out syncWriter
	ui.SetStreams(pr, &out, &out)
	t.Cleanup(func() {
		_ = pw.Close()
		ui.SetDefault(old)
		ui.ResetStreams()
		ui.ClearSpacer()
		ui.DiscardOpenCard()
	})
	return &out, pw
}

// TestRunFormInSessionSubmitAndCancel covers the cli half of the form
// seam: inside a session runForm embeds the form in the shell rather
// than launching huh's own program, a submitted form returns nil, and
// an aborted one maps huh.ErrUserAborted to ErrCancelled — the
// ErrCancelled propagation issue #31 lists as a constraint to
// preserve.
func TestRunFormInSessionSubmitAndCancel(t *testing.T) {
	_, pw := sessionFormStreams(t)

	press := func(b byte) {
		time.Sleep(400 * time.Millisecond)
		_, _ = pw.Write([]byte{b})
	}

	err := ui.RunSession(func() error {
		if !ui.InSession() {
			return errors.New("runForm would not take the session path")
		}

		confirmed := false
		go press('\r')
		if err := runForm(newConfirm().Value(&confirmed)); err != nil {
			return err
		}

		var second bool
		go press(0x03)
		if err := runForm(newConfirm().Value(&second)); !errors.Is(err, ErrCancelled) {
			return errors.New("want ErrCancelled from an aborted form, got: " + errText(err))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
}

// TestRunFormInSessionMultiField covers the multi-field branch of the
// seam, which selects the prologue shape.
func TestRunFormInSessionMultiField(t *testing.T) {
	_, pw := sessionFormStreams(t)

	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = pw.Write([]byte{'\r'})
		time.Sleep(200 * time.Millisecond)
		_, _ = pw.Write([]byte{'\r'})
	}()

	var a, b bool
	err := ui.RunSession(func() error {
		return runForm(newConfirm().Value(&a), newConfirm().Value(&b))
	})
	if err != nil {
		t.Fatalf("RunSession error: %v", err)
	}
}

// TestRunFormOutsideSessionUnchanged locks the non-session path: with
// no session running, runForm still refuses cleanly when the streams
// can't host a prompt, rather than reaching for the shell.
func TestRunFormOutsideSessionUnchanged(t *testing.T) {
	old := ui.Default()
	ui.SetDefault(ui.NewCardReporter())
	var out bytes.Buffer
	ui.SetStreams(nil, &out, &out)
	t.Cleanup(func() {
		ui.SetDefault(old)
		ui.ResetStreams()
	})
	// ui.Input defaults to os.Stdin under `go test`, which is not a
	// TTY, so the guard fires before any program is built.
	if err := runForm(newConfirm()); err == nil {
		t.Fatal("expected the non-interactive guard to refuse")
	}
	if ui.InSession() {
		t.Error("no session should be active")
	}
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return strings.TrimSpace(err.Error())
}
