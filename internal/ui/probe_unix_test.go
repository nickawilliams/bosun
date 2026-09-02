//go:build !windows

package ui

import (
	"os"
	"testing"
	"time"
)

// pipePair returns a connected read/write pair standing in for the
// terminal: the test writes "terminal answers" into w and the probe
// reads them from r.
func pipePair(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return r, w
}

func TestProbeExchangePositiveAnswer(t *testing.T) {
	r, w := pipePair(t)
	if _, err := w.WriteString("\x1bP1+r5463\x1b\\" + da1); err != nil {
		t.Fatal(err)
	}
	if !probeExchange(int(r.Fd()), time.Now().Add(time.Second)) {
		t.Error("probeExchange = false, want true on a Tc answer")
	}
}

func TestProbeExchangeDA1Only(t *testing.T) {
	r, w := pipePair(t)
	if _, err := w.WriteString(da1); err != nil {
		t.Fatal(err)
	}
	if probeExchange(int(r.Fd()), time.Now().Add(time.Second)) {
		t.Error("probeExchange = true, want false when only DA1 answers")
	}
}

func TestProbeExchangeLeavesPostBarrierBytes(t *testing.T) {
	// Keystrokes arriving after the DA1 barrier must stay in the
	// kernel buffer — the probe stops reading at DA1's final byte,
	// so the next consumer of the stream still sees them.
	r, w := pipePair(t)
	if _, err := w.WriteString(da1 + "XYZ"); err != nil {
		t.Fatal(err)
	}
	probeExchange(int(r.Fd()), time.Now().Add(time.Second))

	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("reading leftover: %v", err)
	}
	if got := string(buf[:n]); got != "XYZ" {
		t.Errorf("leftover = %q, want %q", got, "XYZ")
	}
}

func TestProbeExchangeTimeout(t *testing.T) {
	// A mute terminal: nothing to read. The exchange must give up at
	// the deadline and report no upgrade rather than blocking.
	r, _ := pipePair(t)
	start := time.Now()
	if probeExchange(int(r.Fd()), start.Add(50*time.Millisecond)) {
		t.Error("probeExchange = true, want false on timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %v, want prompt return", elapsed)
	}
}

func TestProbeExchangeEOF(t *testing.T) {
	// The write side closing mid-probe (terminal gone) must
	// terminate the loop with whatever was parsed, not spin or hang.
	r, w := pipePair(t)
	if _, err := w.WriteString("\x1bP1+r524742\x1b\\"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !probeExchange(int(r.Fd()), time.Now().Add(time.Second)) {
		t.Error("probeExchange = false, want true — the RGB answer landed before EOF")
	}
}

func TestProbeTerminalNonTTY(t *testing.T) {
	// probeTerminal's first act is the raw-mode toggle; on a
	// non-terminal fd it must fail closed — no upgrade, no query
	// bytes written, and a prompt return rather than a timeout wait.
	r, w := pipePair(t)
	start := time.Now()
	if probeTerminal(r, w) {
		t.Error("probeTerminal = true on a pipe, want false")
	}
	if elapsed := time.Since(start); elapsed > probeTimeout/2 {
		t.Errorf("took %v, want prompt failure before the read loop", elapsed)
	}
}

func TestWaitReadableExpiredDeadline(t *testing.T) {
	r, _ := pipePair(t)
	if waitReadable(int(r.Fd()), time.Now().Add(-time.Second)) {
		t.Error("waitReadable = true on an already-expired deadline, want false")
	}
}

func TestWaitReadableRejectsOutOfRangeFd(t *testing.T) {
	// FdSet.Set panics at or beyond FD_SETSIZE; waitReadable must
	// refuse such fds instead of crashing the process at startup.
	if waitReadable(4096, time.Now().Add(time.Second)) {
		t.Error("waitReadable = true for an out-of-range fd, want false")
	}
	if waitReadable(-1, time.Now().Add(time.Second)) {
		t.Error("waitReadable = true for a negative fd, want false")
	}
}

func TestSameTerminal(t *testing.T) {
	r, w := pipePair(t)
	if !sameTerminal(int(r.Fd()), int(r.Fd())) {
		t.Error("sameTerminal = false for one fd twice, want true")
	}
	// Two pipe ends share an Rdev of 0, so the mismatch case needs a
	// real character device on one side.
	devnull, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })
	if sameTerminal(int(r.Fd()), int(devnull.Fd())) {
		t.Error("sameTerminal = true for a pipe vs /dev/null, want false")
	}
	// An invalid fd on either side must fail closed.
	if sameTerminal(int(r.Fd()), -1) {
		t.Error("sameTerminal = true with an invalid second fd, want false")
	}
	if sameTerminal(-1, int(r.Fd())) {
		t.Error("sameTerminal = true with an invalid first fd, want false")
	}
	_ = w
}
