package testharness

import (
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

// chunkReader is the harness's stdin. Each Type call appends one
// chunk; Read serves at most one chunk at a time and pauses at chunk
// boundaries (see chunkPause). Within a chunk, keys are delivered
// back-to-back exactly like the plain buffer this replaces — the
// boundary is the synchronization point, so callers group "keys the
// currently focused field consumes" per Type call.
//
// When all chunks are drained, Read returns io.EOF, matching
// bytes.Buffer semantics (a form still waiting for input aborts).
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
}

// NextConsumer marks a consumer-session boundary (called by
// ui.Input() when a form takes over the stream and by
// ui.ReleaseInput() when it exits). Reads in flight from earlier
// sessions will return io.EOF instead of data.
func (r *chunkReader) NextConsumer() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session++
}

// append adds one chunk to be served after all prior chunks.
func (r *chunkReader) append(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, []byte(s))
}

func (r *chunkReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	session := r.session
	if len(r.chunks) == 0 {
		r.mu.Unlock()
		return 0, io.EOF
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
