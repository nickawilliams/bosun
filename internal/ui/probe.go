package ui

// Startup truecolor probe (issue #106).
//
// colorprofile.Detect answers from what the terminal *advertises*
// (COLORTERM, terminfo, tmux info). Terminals that render truecolor
// without advertising it — tmux without Tc/RGB overrides, SSH
// sessions that don't forward COLORTERM, embedded emulators — get
// uniformly quantized output. When env detection answers below
// TrueColor in auto mode, this probe asks the terminal itself
// (Neovim prior art): XTGETTCAP queries for the RGB and Tc
// capabilities, immediately followed by DA1 (Primary Device
// Attributes, CSI c) as a barrier. Essentially every terminal
// answers DA1, and queries are answered in order — when the DA1
// answer arrives, the XTGETTCAP answer either came before it or is
// never coming, so the probe is fast and mostly timeout-free: one
// terminal round-trip (sub-millisecond locally, one network RTT over
// SSH), with the timeout paid only by terminals that ignore DA1.
//
// The probe only ever upgrades. A positive answer re-pins the
// profile to TrueColor through the applyDetectedProfile seam before
// the first styled byte; a silent, negative, or garbled answer
// leaves the env-detected profile untouched. Forced modes and
// NO_COLOR never reach the probe (autoColorMode gates), nor do
// non-TTY streams, raw/plain rendering (the Bootstrap call site sits
// on the interactive branch only), or CI-shaped environments.

import (
	"bytes"
	"encoding/hex"
	"os"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// autoColorMode records whether the last ApplyColorMode resolved the
// profile via auto detection, as opposed to a forced mode or
// NO_COLOR. Only that case is eligible for the startup probe. Same
// single-goroutine init contract as OutputProfile.
var autoColorMode bool

// probeQuery is the byte sequence written to the terminal: XTGETTCAP
// for "RGB" (hex 524742) and "Tc" (hex 5463) as separate queries —
// safer across implementations than one combined query — then DA1
// (CSI c) as the ordering barrier.
const probeQuery = "\x1bP+q524742\x1b\\" + "\x1bP+q5463\x1b\\" + "\x1b[c"

// ProbeTruecolorUpgrade refines an auto-detected below-TrueColor
// profile by interrogating the terminal directly. Called by
// cli.Bootstrap after ApplyColorMode, on the interactive rendering
// branch only, so raw/plain output never sees query bytes.
//
// Preconditions, all structural:
//
//   - Auto color mode resolved via detection (forced modes and
//     NO_COLOR leave autoColorMode false).
//   - Detection answered ANSI or ANSI256. At Ascii and below the
//     environment gives no evidence the terminal understands DCS at
//     all, and stray probe bytes on a dumb terminal are visible
//     garbage; at TrueColor there is nothing to refine.
//   - Stdin and stdout are both real TTYs — the raw-mode read needs
//     the former, the query write the latter. Injected test buffers
//     and pipes both fail this check.
//   - Not a CI-shaped environment, where a stray query could pollute
//     logs captured through a PTY.
//
// A positive answer re-pins the profile through the same
// applyDetectedProfile seam ApplyColorMode used, and exports
// COLORTERM=truecolor for this process so fang's independent env
// detection reaches the same answer (the help-styling caveat in
// cli.FangColorScheme; fang exposes no way to inject the pinned
// profile, but it does read the environment we now know to be
// under-advertised).
//
// Hard limit, per the issue: a terminal that renders truecolor but
// implements neither COLORTERM nor XTGETTCAP is indistinguishable
// from one that can't — the only evidence channels are the
// terminal's own declarations. The `color: truecolor` override
// remains the answer there.
func ProbeTruecolorUpgrade() {
	if !autoColorMode {
		return
	}
	if OutputProfile != colorprofile.ANSI && OutputProfile != colorprofile.ANSI256 {
		return
	}
	in, ok := defaultInput.(*os.File)
	if !ok || !term.IsTerminal(int(in.Fd())) {
		return
	}
	out, ok := defaultOutput.(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return
	}
	if os.Getenv("CI") != "" {
		return
	}
	if !probeTerminal(in, out) {
		return
	}
	applyTruecolorUpgrade()
}

// applyTruecolorUpgrade re-pins the profile to TrueColor through the
// applyDetectedProfile seam and exports COLORTERM for this process.
// The export is what makes fang's independent env detection agree
// with the probe's answer — and it is *accurate*: a probe hit means
// the environment under-advertises a capability the terminal just
// confirmed, so child processes inherit a truthful declaration.
func applyTruecolorUpgrade() {
	applyDetectedProfile(colorprofile.TrueColor)
	rebuildStyles()
	_ = os.Setenv("COLORTERM", "truecolor")
}

// probeState enumerates the parser's positions inside the reply
// byte stream.
type probeState int

const (
	probeGround probeState = iota // hunting for ESC
	probeEsc                      // saw ESC, expecting an opener
	probeCSI                      // inside CSI, collecting until final byte
	probeDCS                      // inside DCS, collecting until ST
	probeDCSEsc                   // inside DCS, saw ESC (ST's first byte?)
)

// maxProbePayload bounds sequence buffers so a terminal streaming
// bytes without a terminator cannot grow memory; an oversized
// sequence is discarded wholesale and the parser falls back to
// hunting for the next ESC.
const maxProbePayload = 4096

// probeParser incrementally scans the terminal's reply for XTGETTCAP
// and DA1 responses. Anything else — keystrokes the user typed
// during the probe window, unrelated control sequences, a garbled
// DCS from a misbehaving terminal (Apple's Terminal.app is known to
// answer XTGETTCAP incorrectly) — is skipped without effect: an
// unparseable answer means "no upgrade", never a crash. feed reports
// how many bytes it consumed so the read loop can stop exactly at
// the DA1 terminator and never touch bytes that follow it.
type probeParser struct {
	state   probeState
	payload []byte
	// truecolor is set once a well-formed XTGETTCAP reply
	// (DCS 1+r … ST) names the RGB or Tc capability.
	truecolor bool
}

// feed advances the parser over b, reporting how many bytes it
// consumed and whether the DA1 barrier completed. On done, n points
// one past DA1's final byte; the caller must not feed (or read)
// further.
func (p *probeParser) feed(b []byte) (n int, done bool) {
	for i, c := range b {
		if p.step(c) {
			return i + 1, true
		}
	}
	return len(b), false
}

// step advances the state machine one byte; reports whether the DA1
// barrier completed.
func (p *probeParser) step(c byte) bool {
	switch p.state {
	case probeGround:
		if c == 0x1b {
			p.state = probeEsc
		}
		// Anything else is a queued keystroke or noise: skip.
	case probeEsc:
		switch c {
		case 'P':
			p.state, p.payload = probeDCS, p.payload[:0]
		case '[':
			p.state, p.payload = probeCSI, p.payload[:0]
		case 0x1b:
			// ESC ESC: the second is still a potential opener.
		default:
			p.state = probeGround
		}
	case probeCSI:
		switch {
		case c >= 0x40 && c <= 0x7e:
			// Final byte. DA1's response is CSI ? … c; requiring the
			// ? prefix keeps arrow-key input (CSI A) and an echoed
			// bare query (CSI c) from faking the barrier.
			p.state = probeGround
			if c == 'c' && len(p.payload) > 0 && p.payload[0] == '?' {
				return true
			}
		case c == 0x1b:
			// Aborted sequence; the ESC opens a new one.
			p.state = probeEsc
		case c >= 0x20 && c <= 0x3f:
			p.appendPayload(c)
		default:
			// C0 controls inside CSI: ignore.
		}
	case probeDCS:
		switch c {
		case 0x1b:
			p.state = probeDCSEsc
		case 0x07:
			// BEL: nonstandard DCS terminator some terminals emit.
			p.state = probeGround
			p.parseCapabilityReply()
		default:
			p.appendPayload(c)
		}
	case probeDCSEsc:
		if c == '\\' {
			// ST: the DCS is complete.
			p.state = probeGround
			p.parseCapabilityReply()
			return false
		}
		// ESC inside DCS not followed by \ is a garbled, unterminated
		// sequence (the Terminal.app hazard). Abandon it and
		// reprocess the byte as if the ESC opened a fresh sequence —
		// so a DA1 answer arriving right behind the garbage still
		// terminates the probe.
		p.payload = p.payload[:0]
		p.state = probeEsc
		return p.step(c)
	}
	return false
}

// appendPayload grows the current sequence buffer, discarding the
// whole sequence if it exceeds maxProbePayload.
func (p *probeParser) appendPayload(c byte) {
	if len(p.payload) >= maxProbePayload {
		p.payload = p.payload[:0]
		p.state = probeGround
		return
	}
	p.payload = append(p.payload, c)
}

// parseCapabilityReply inspects a completed DCS payload for a
// positive XTGETTCAP answer: `1+r` followed by ;-separated
// hex-encoded name[=value] pairs. A reply naming RGB or Tc confirms
// truecolor. `0+r` (capability unknown), foreign capability names,
// and undecodable hex are all "no" — a misbehaving terminal can
// garble its answer but never force an upgrade.
func (p *probeParser) parseCapabilityReply() {
	rest, ok := bytes.CutPrefix(p.payload, []byte("1+r"))
	if !ok {
		return
	}
	for _, pair := range bytes.Split(rest, []byte(";")) {
		name, _, _ := bytes.Cut(pair, []byte("="))
		decoded, err := hex.DecodeString(string(name))
		if err != nil {
			continue
		}
		switch string(decoded) {
		case "RGB", "Tc":
			p.truecolor = true
		}
	}
}
