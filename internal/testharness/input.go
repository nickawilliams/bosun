package testharness

import (
	"errors"
	"io"
	"sync"
	"time"
)

// chunkPause is how long chunkReader waits before serving each chunk
// after the first.
//
// Why a pause at all: huh advances fields asynchronously — the focused
// field returns huh.NextField as a tea.Cmd, and bubbletea delivers the
// resulting nextFieldMsg from a separate goroutine (tea.go's
// `go p.Send(cmd())`). Keys served after a field's final Enter race
// that delivery and can land in the still-focused field.
//
// Why a pause is sufficient: bubbletea's input driver loop is
// read → parse → send → read, and the message channel is unbuffered,
// so by the time Read is called for the next chunk the event loop has
// already received the previous chunk's final key. The pause only has
// to cover finishing that key's Update plus one goroutine hop for the
// nextFieldMsg — microseconds in normal runs.
//
// Why this generous: there is nothing to synchronize on — with a
// non-TTY output bubbletea uses the nil renderer, so the transition
// produces no observable output — and under the race detector,
// goroutine scheduling is perturbed enough that a 20ms pause was
// observed to lose the race. 200ms buys an order of magnitude of
// margin and is paid only per Type-call boundary, not per test.
const chunkPause = 200 * time.Millisecond

// starvationGrace is how long a live form's read may sit on an empty
// queue before the harness calls it starved (see errStarvedInput).
//
// The wait exists for exactly one race: a form's read loop can be
// parked on the next read while the form is still tearing down after
// its final keystroke. That teardown ends in ui.ReleaseInput, which
// bumps the session and releases the parked read with io.EOF — so the
// grace only has to outlast form teardown, never a command's own work
// between prompts (a read cannot be parked while no form is running).
//
// It is therefore paid only by runs that genuinely hang. Two seconds
// is orders of magnitude above observed teardown even under the race
// detector, and still ~1% of the package timeout a starved run used to
// burn. Raise it if a loaded machine ever trips a false positive; the
// symptom would be a starvation failure naming a prompt the scenario
// demonstrably did feed.
const starvationGrace = 2 * time.Second

// errStarvedInput is what a starved read returns instead of blocking
// forever. It must not be (or wrap) io.EOF: ultraviolet's
// TerminalReader reports EOF as a clean end of input and returns nil,
// so bubbletea's read loop retires quietly while the form waits on a
// key that can never come — precisely the hang this replaces. Any
// other error travels StreamEvents → Program.errs → eventLoop, and
// the program returns it, so the form unwinds and the command
// returns.
//
// This error's TEXT, though, does not survive the trip: bubbletea
// wraps a killed program's cause as ErrProgramKilled, and huh maps
// that to its own ErrTimeout — discarding the cause. So a command
// under a starved harness fails with huh's bare "timeout", which
// explains nothing. That is why the diagnosis lives in Harness.Run's
// starvation report rather than in the error the command returns;
// this value stays descriptive for the day upstream stops flattening
// it, and is what the harness's own tests match on.
var errStarvedInput = errors.New(
	"testharness: the command asked for input the scenario never fed " +
		"(no queued Type() input remains)")

// chunkReader is the harness's stdin. Each Type call appends one
// chunk; Read serves at most one chunk at a time and pauses at chunk
// boundaries (see chunkPause). Within a chunk, keys are delivered
// back-to-back exactly like the plain buffer this replaces — the
// boundary is the synchronization point, so callers group "keys the
// currently focused field consumes" per Type call.
//
// When the queue is drained, Read BLOCKS rather than returning io.EOF
// — real stdin blocks awaiting a keystroke, and returning EOF here is
// what used to hang the run: bubbletea's input loop ends quietly on
// EOF while the form waits for a key that can never arrive, so an
// under-fed scenario burned the package's whole test timeout with no
// error surfaced. A blocked read has three exits:
//
//   - the session is bumped (the form that owned the read finished, or
//     another took the stream): io.EOF, nothing consumed;
//   - release: Harness.Run is over, so parked readers can retire;
//   - starvationGrace elapses with the read still live and the queue
//     still empty: errStarvedInput, and starved is latched so
//     Harness.Run can name the failure.
//
// Only the third is a test failure, and the session bump is what makes
// that distinction sound — see starvationGrace.
//
// Session tracking: bubbletea cannot cancel reads on a non-File
// reader, so a finished form leaks a read-loop goroutine blocked in
// Read — and a post-cancel read that completes gets its data
// DISCARDED by the cancelreader fallback. With chunks pre-queued,
// that stale reader would consume the chunk meant for the next form
// in the same Run (form → plan gate) and the run would hang waiting
// for input. The session is bumped at both consumer boundaries —
// ui.Input() when a form takes the stream, ui.ReleaseInput() when it
// exits — and a Read that began under an older session returns
// io.EOF without consuming, so the pending chunk is delivered to the
// live form instead. The exit-side bump is what makes this hold
// regardless of how much work runs between two prompts: the leaked
// read is in flight before the form returns, so its session snapshot
// is stale the moment runForm exits. The remaining assumption is only
// that form teardown completes within chunkPause — a property of the
// form machinery, not of the command's between-prompt work.
type chunkReader struct {
	mu     sync.Mutex
	chunks [][]byte
	// served: any byte has ever been served (the very first chunk gets
	// no leading pause). started: the head chunk is partially served,
	// so the next Read continues it rather than beginning a new chunk.
	served  bool
	started bool
	// session increments on every NextConsumer call; reads that began
	// under an older session are refused without consuming.
	session uint64
	// wake is closed (and replaced) on every state change a blocked
	// read cares about — an appended chunk, a session bump, a release.
	// sync.Cond would fit except that it cannot wait with a timeout,
	// and the starvation grace is the whole point.
	wake chan struct{}
	// queued counts Type calls, for the starvation report. There is no
	// consumed counterpart because starvation implies an empty queue,
	// which implies every chunk was served — the interesting number is
	// how many the scenario thought would be enough.
	queued int
	// starveAfter is starvationGrace, overridable by this package's own
	// tests so they don't pay the full grace per case.
	starveAfter time.Duration
	// starved latches the first starvation. Later reads fail fast on it
	// rather than waiting again: a command that swallows a prompt error
	// walks straight into its next prompt, and one grace per remaining
	// prompt would put the hang back.
	starved bool
	// released marks the end of a Run. Parked readers (a finished
	// form's leaked read loop, most often) retire on it instead of
	// sitting out the grace and latching a starvation the run never
	// suffered.
	released bool
}

// newChunkReader returns a reader with the default starvation grace.
func newChunkReader() *chunkReader {
	return &chunkReader{
		wake:        make(chan struct{}),
		starveAfter: starvationGrace,
	}
}

// notify wakes every blocked read. Call with the lock held.
func (r *chunkReader) notify() {
	close(r.wake)
	r.wake = make(chan struct{})
}

// NextConsumer marks a consumer-session boundary (called by
// ui.Input() when a form takes over the stream and by
// ui.ReleaseInput() when it exits). Reads in flight from earlier
// sessions will return io.EOF instead of data.
func (r *chunkReader) NextConsumer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session++
	r.notify()
}

// append adds one chunk to be served after all prior chunks.
func (r *chunkReader) append(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, []byte(s))
	r.queued++
	r.notify()
}

// beginRun clears the per-run state so a harness reused across several
// Run calls starts each one un-starved and un-released. Queued input
// deliberately survives: Type may be called once for a whole test.
func (r *chunkReader) beginRun() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starved, r.released = false, false
}

// release ends a run: parked reads retire with io.EOF rather than
// waiting out the grace against a command that already returned.
func (r *chunkReader) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = true
	r.notify()
}

// starvation reports whether the run starved, how much input the
// scenario had queued, and the grace the read waited out.
func (r *chunkReader) starvation() (starved bool, queued int, grace time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starved, r.queued, r.starveAfter
}

func (r *chunkReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	session := r.session
	if err := r.awaitChunk(session); err != nil {
		r.mu.Unlock()
		return 0, err
	}
	pause := r.served && !r.started
	r.mu.Unlock()

	// Pause outside the lock so a concurrent append isn't blocked.
	if pause {
		time.Sleep(chunkPause)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != session {
		// A newer consumer owns the stream: this read belongs to an
		// exited form's leaked read loop. Refuse without consuming so
		// the pending chunk reaches the live form.
		return 0, io.EOF
	}
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[0])
	r.chunks[0] = r.chunks[0][n:]
	r.served, r.started = true, true
	if len(r.chunks[0]) == 0 {
		r.chunks = r.chunks[1:]
		r.started = false
	}
	return n, nil
}

// awaitChunk blocks until there is a chunk to serve, returning a
// non-nil error when this read should give up instead. Called and
// returns with r.mu held; the wait itself drops the lock.
//
// session is the caller's snapshot: once it goes stale the read is a
// leaked form's, and the queue — empty or not — is not its business.
func (r *chunkReader) awaitChunk(session uint64) error {
	var timeout <-chan time.Time
	for len(r.chunks) == 0 {
		switch {
		case r.released, r.session != session:
			return io.EOF
		case r.starved:
			return errStarvedInput
		}
		if timeout == nil {
			// Started on the first empty observation and never reset,
			// so the grace bounds the whole wait rather than each hop.
			t := time.NewTimer(r.starveAfter)
			defer t.Stop()
			timeout = t.C
		}
		wake := r.wake
		r.mu.Unlock()
		select {
		case <-wake:
		case <-timeout:
			r.mu.Lock()
			// The state can have moved while the timer ran; the loop
			// head re-reads it and only latches if nothing did.
			if len(r.chunks) == 0 && !r.released && r.session == session {
				r.starved = true
			}
			continue
		}
		r.mu.Lock()
	}
	return nil
}
