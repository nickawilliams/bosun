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
// sub-millisecond; over SSH it is one network RTT. Only a terminal
// that ignores DA1 entirely — vanishingly rare — pays the full
// timeout.
const probeTimeout = 500 * time.Millisecond

// probeTerminal performs the raw-mode query exchange against a real
// terminal. Raw mode turns off echo and line buffering so the reply
// is readable byte-by-byte and never renders; the previous state is
// restored before returning. Reports a positive XTGETTCAP answer.
func probeTerminal(in, out *os.File) bool {
	fd := int(in.Fd())
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

// waitReadable blocks until fd has readable data or the deadline
// passes. select(2) rather than poll(2): macOS's poll historically
// misreports character devices, and the probe's fds (stdin, test
// pipes) sit well below FD_SETSIZE.
func waitReadable(fd int, deadline time.Time) bool {
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
