//go:build !windows

package ui

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// probeTimeout bounds the whole exchange for terminals that answer
// neither XTGETTCAP nor DA1. Locally the round-trip is
// sub-millisecond; over SSH it is one network RTT — and the timeout
// must comfortably exceed the worst plausible RTT, not just the
// typical one: a reply that arrives *after* the probe gave up and
// restored the terminal lands in the input queue as if typed,
// echoed at the prompt and delivered to the next reader. One second
// clears even satellite-grade links (~600ms RTT), and only a
// terminal that ignores DA1 entirely — vanishingly rare — ever pays
// the full wait. The residual hazard is inherent to probing from a
// short-lived process: a bounded post-timeout drain cannot outlast
// an unboundedly late reply, and holding stdin open in the
// background would steal keystrokes from the forms that run next
// (the InputHandoff scar tissue).
const probeTimeout = time.Second

// probeTerminal performs the raw-mode query exchange against a real
// terminal. Raw mode turns off echo and line buffering so the reply
// is readable byte-by-byte and never renders; the previous state is
// restored before returning. Reports a positive XTGETTCAP answer.
func probeTerminal(in, out *os.File) bool {
	fd := int(in.Fd())
	// The query goes to out and the reply arrives on in; when they
	// are different terminals (stdout redirected to another tty),
	// the reply would land in the *other* terminal's input queue —
	// injected at whatever prompt owns it. Only interrogate a
	// terminal we can hear answer.
	if !sameTerminal(fd, int(out.Fd())) {
		return false
	}
	prev, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer func() { _ = term.Restore(fd, prev) }()

	if _, err := out.WriteString(probeQuery); err != nil {
		return false
	}
	return probeExchange(fd, time.Now().Add(probeTimeout))
}

// probeExchange reads the terminal's reply one byte at a time until
// the DA1 barrier, a read failure, or the deadline. Byte-granular
// reads are deliberate: the loop stops exactly at DA1's final byte,
// so anything after it — keystrokes the user typed once the probe
// was answered — is never pulled out of the kernel's input queue.
// Bytes typed *during* the probe window arrive before DA1 and are
// consumed with the response; that window is one terminal
// round-trip, and dropping them matches the prior art (Neovim's
// startup interrogation does the same).
//
// Split from probeTerminal so tests can drive it with a pipe: the
// wait/read loop and the stop-at-DA1 consumption contract are
// covered without a PTY; only the raw-mode toggle needs one.
func probeExchange(fd int, deadline time.Time) bool {
	var p probeParser
	buf := make([]byte, 1)
	for {
		if !waitReadable(fd, deadline) {
			return p.truecolor
		}
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n <= 0 {
			return p.truecolor
		}
		if _, done := p.feed(buf[:n]); done {
			return p.truecolor
		}
	}
}

// sameTerminal reports whether both fds refer to the same underlying
// character device.
func sameTerminal(fd1, fd2 int) bool {
	var st1, st2 unix.Stat_t
	if err := unix.Fstat(fd1, &st1); err != nil {
		return false
	}
	if err := unix.Fstat(fd2, &st2); err != nil {
		return false
	}
	return st1.Rdev == st2.Rdev
}

// waitReadable blocks until fd has readable data or the deadline
// passes. select(2) rather than poll(2): macOS's poll historically
// misreports character devices, and the probe's fds (stdin, test
// pipes) sit well below FD_SETSIZE.
func waitReadable(fd int, deadline time.Time) bool {
	// FdSet.Set indexes past its bitmap — a panic — at or beyond
	// FD_SETSIZE. Production fds are 0/1 by the time the gates
	// pass; refuse anything a select set cannot hold rather than
	// trusting that forever.
	if fd < 0 || fd >= 1024 {
		return false
	}
	for {
		d := time.Until(deadline)
		if d <= 0 {
			return false
		}
		var set unix.FdSet
		set.Set(fd)
		tv := unix.NsecToTimeval(int64(d))
		n, err := unix.Select(fd+1, &set, nil, nil, &tv)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && n > 0
	}
}
