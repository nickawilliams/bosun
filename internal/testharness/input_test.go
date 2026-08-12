package testharness

import (
	"errors"
	"io"
	"testing"
	"time"

	"charm.land/huh/v2"
)

// testGrace is the starvation grace these tests run with. Long enough
// that a scheduling hiccup under -race can't fire it early, short
// enough that a case which waits it out stays cheap.
const testGrace = 300 * time.Millisecond

// newTestReader returns a chunkReader with the shortened grace.
func newTestReader() *chunkReader {
	r := newChunkReader()
	r.starveAfter = testGrace
	return r
}

// readAll drains one Read into a string.
func readOnce(t *testing.T, r *chunkReader) (string, error) {
	t.Helper()
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	return string(buf[:n]), err
}

func TestChunkReaderServesChunksInOrder(t *testing.T) {
	r := newTestReader()
	r.append("ab")
	r.append("cd")

	for _, want := range []string{"ab", "cd"} {
		got, err := readOnce(t, r)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got != want {
			t.Fatalf("Read = %q, want %q", got, want)
		}
	}

	if _, queued, _ := r.starvation(); queued != 2 {
		t.Errorf("queued = %d, want 2", queued)
	}
}

// A read that began under an older session must refuse a pending
// chunk rather than swallow it — the leaked-read-loop guard. The first
// read is what arms the inter-chunk pause, which is the window the
// session bump lands in.
func TestChunkReaderStaleSessionRefusesPendingChunk(t *testing.T) {
	r := newTestReader()
	r.append("first")
	r.append("second")

	if got, err := readOnce(t, r); err != nil || got != "first" {
		t.Fatalf("first Read = %q, %v; want %q, nil", got, err, "first")
	}

	type result struct {
		s   string
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := readOnce(t, r)
		done <- result{s, err}
	}()

	// The stale read is parked in chunkPause; bump before it lands.
	time.Sleep(chunkPause / 4)
	r.NextConsumer()

	got := <-done
	if !errors.Is(got.err, io.EOF) || got.s != "" {
		t.Fatalf("stale Read = %q, %v; want \"\", io.EOF", got.s, got.err)
	}
	if fresh, err := readOnce(t, r); err != nil || fresh != "second" {
		t.Fatalf("fresh Read = %q, %v; want %q, nil", fresh, err, "second")
	}
}

// The defect this whole mechanism exists for: a live consumer reading
// a drained queue must fail, not hang.
func TestChunkReaderStarvesOnDrainedQueue(t *testing.T) {
	r := newTestReader()

	start := time.Now()
	if _, err := readOnce(t, r); !errors.Is(err, errStarvedInput) {
		t.Fatalf("Read err = %v, want errStarvedInput", err)
	}
	if elapsed := time.Since(start); elapsed < testGrace/2 {
		t.Errorf("starved after %s, want at least ~%s of grace", elapsed, testGrace)
	}

	starved, _, _ := r.starvation()
	if !starved {
		t.Error("starvation() = false, want true")
	}

	// Latched: a command that swallows the prompt error walks into its
	// next prompt, and re-serving the grace each time would restore the
	// hang one prompt at a time.
	start = time.Now()
	if _, err := readOnce(t, r); !errors.Is(err, errStarvedInput) {
		t.Fatalf("second Read err = %v, want errStarvedInput", err)
	}
	if elapsed := time.Since(start); elapsed > testGrace/2 {
		t.Errorf("second Read waited %s; want it to fail fast on the latch", elapsed)
	}
}

// The false-positive guard: a finished form's read loop is parked on
// the drained queue too, and its form's exit — not the grace — is what
// must retire it.
func TestChunkReaderSessionBumpRetiresParkedRead(t *testing.T) {
	r := newTestReader()

	done := make(chan error, 1)
	go func() {
		_, err := readOnce(t, r)
		done <- err
	}()

	time.Sleep(testGrace / 4)
	r.NextConsumer() // ui.ReleaseInput on form exit

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("parked Read err = %v, want io.EOF", err)
		}
	case <-time.After(testGrace * 4):
		t.Fatal("parked Read never returned after the session bump")
	}

	if starved, _, _ := r.starvation(); starved {
		t.Error("starvation() = true; a retired read must not latch starvation")
	}
}

// Same guard on the other boundary: Run is over, so a still-parked
// read must retire rather than sit out the grace and blame the run.
func TestChunkReaderReleaseRetiresParkedRead(t *testing.T) {
	r := newTestReader()

	done := make(chan error, 1)
	go func() {
		_, err := readOnce(t, r)
		done <- err
	}()

	time.Sleep(testGrace / 4)
	r.release()

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("parked Read err = %v, want io.EOF", err)
		}
	case <-time.After(testGrace * 4):
		t.Fatal("parked Read never returned after release")
	}
	if starved, _, _ := r.starvation(); starved {
		t.Error("starvation() = true; a released read must not latch starvation")
	}
}

// Blocking must not mean deadlock: input queued while a read is parked
// wakes it.
func TestChunkReaderAppendWakesParkedRead(t *testing.T) {
	r := newTestReader()

	type result struct {
		s   string
		err error
	}
	done := make(chan result, 1)
	go func() {
		s, err := readOnce(t, r)
		done <- result{s, err}
	}()

	time.Sleep(testGrace / 4)
	r.append("late")

	select {
	case got := <-done:
		if got.err != nil || got.s != "late" {
			t.Fatalf("parked Read = %q, %v; want %q, nil", got.s, got.err, "late")
		}
	case <-time.After(testGrace * 4):
		t.Fatal("parked Read never woke on the appended chunk")
	}
}

// beginRun clears the latch so a harness reused for several Runs
// doesn't carry one run's starvation into the next.
func TestChunkReaderBeginRunClearsLatch(t *testing.T) {
	r := newTestReader()
	if _, err := readOnce(t, r); !errors.Is(err, errStarvedInput) {
		t.Fatalf("Read err = %v, want errStarvedInput", err)
	}

	r.beginRun()
	if starved, _, _ := r.starvation(); starved {
		t.Fatal("starvation() = true after beginRun, want false")
	}
	r.append("x")
	if got, err := readOnce(t, r); err != nil || got != "x" {
		t.Fatalf("Read after beginRun = %q, %v; want %q, nil", got, err, "x")
	}
}

// The load-bearing claim: a real bubbletea form driven by a starved
// reader UNWINDS. io.EOF here would leave the form running forever
// (ultraviolet reports EOF as a clean end of input and bubbletea's
// read loop just retires), which is the hang this replaces — so this
// pins propagation through StreamEvents → Program → Form.Run, not
// just the reader's own behaviour.
//
// It also pins what the command actually sees. huh maps a killed
// program to its own ErrTimeout and drops the cause, so errStarvedInput
// never reaches the caller — the reason the diagnosis lives in
// Harness.Run's starvation report instead. If a huh upgrade stops
// flattening the cause this fails, and the harness can start relying
// on the error text too.
func TestStarvedReaderUnblocksLiveForm(t *testing.T) {
	r := newTestReader()

	var answer string
	form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Unfed").Value(&answer))).
		WithInput(r).
		WithOutput(io.Discard).
		WithWidth(80)

	done := make(chan error, 1)
	go func() { done <- form.Run() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("form.Run returned nil; a starved form must fail")
		}
		if !errors.Is(err, huh.ErrTimeout) {
			t.Errorf("form.Run err = %v, want huh.ErrTimeout (upstream's "+
				"flattening of errStarvedInput)", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("form.Run never returned: a starved reader still hangs the form")
	}

	if starved, _, _ := r.starvation(); !starved {
		t.Error("starvation() = false; the harness would not report the hang")
	}
}
