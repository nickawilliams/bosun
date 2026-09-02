package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// resetProbeState restores the probe gate after a test that flips it.
// Same non-parallel contract as resetColorState.
func resetProbeState(t *testing.T) {
	t.Helper()
	prev := autoColorMode
	t.Cleanup(func() { autoColorMode = prev })
}

// da1 is a realistic Primary Device Attributes answer (VT220-class).
const da1 = "\x1b[?62;22c"

func TestProbeParser(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		truecolor bool
		done      bool
	}{
		{
			name:      "RGB hit with value",
			input:     "\x1bP1+r524742=382f382f38\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name:      "Tc hit without value",
			input:     "\x1bP1+r5463\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name:      "combined semicolon reply",
			input:     "\x1bP1+r524742=38;5463=38\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name:      "0+r miss",
			input:     "\x1bP0+r524742\x1b\\\x1bP0+r5463\x1b\\" + da1,
			truecolor: false,
			done:      true,
		},
		{
			name:      "DA1 only, no XTGETTCAP support",
			input:     da1,
			truecolor: false,
			done:      true,
		},
		{
			name: "queued keystrokes and arrow keys before the response",
			// Plain typed bytes plus a CSI A (up-arrow) must be
			// skipped, not consumed as response or barrier.
			input:     "abc\x1b[A\x1bP1+r5463\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name: "garbled unterminated DCS then DA1",
			// Terminal.app-style wrong answer: a DCS that never
			// closes with ST. The embedded ESC [ must be re-read as
			// the DA1 opener, terminating the probe with no upgrade.
			input:     "\x1bP1+r54" + da1,
			truecolor: false,
			done:      true,
		},
		{
			name: "ESC ESC before the response",
			// A doubled ESC (queued partial escape) must leave the
			// second ESC live as the response's opener.
			input:     "\x1b\x1bP1+r5463\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name: "C0 control inside CSI is ignored",
			// A stray C0 byte (here BEL) inside the DA1 response must
			// not abort the sequence or corrupt its payload.
			input:     "\x1b[?62;\x0722c",
			truecolor: false,
			done:      true,
		},
		{
			name: "CSI aborted by ESC mid-sequence",
			// An unterminated CSI (queued garbage) cut off by the
			// response's own ESC: the opener must not be lost.
			input:     "\x1b[12\x1bP1+r5463\x1b\\" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name:      "undecodable hex name",
			input:     "\x1bP1+r5G63\x1b\\" + da1,
			truecolor: false,
			done:      true,
		},
		{
			name: "foreign capability name",
			// hex("smulx") — a real terminfo cap, not a color one.
			input:     "\x1bP1+r736d756c78=1\x1b\\" + da1,
			truecolor: false,
			done:      true,
		},
		{
			name:      "BEL-terminated DCS",
			input:     "\x1bP1+r5463\x07" + da1,
			truecolor: true,
			done:      true,
		},
		{
			name: "bare CSI c is not the barrier",
			// An echoed query (CSI c, no ? prefix) must not fake DA1.
			input:     "\x1b[c",
			truecolor: false,
			done:      false,
		},
		{
			name:      "positive answer but DA1 never arrives",
			input:     "\x1bP1+r5463\x1b\\",
			truecolor: true,
			done:      false,
		},
		{
			name: "oversized DCS payload is discarded",
			input: "\x1bP1+r" + strings.Repeat("5463;", maxProbePayload/4) +
				"\x1b\\" + da1,
			truecolor: false,
			done:      true,
		},
		{
			name:      "empty input",
			input:     "",
			truecolor: false,
			done:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p probeParser
			_, done := p.feed([]byte(tc.input))
			if p.truecolor != tc.truecolor {
				t.Errorf("truecolor = %v, want %v", p.truecolor, tc.truecolor)
			}
			if done != tc.done {
				t.Errorf("done = %v, want %v", done, tc.done)
			}

			// The same input fed one byte at a time must decode
			// identically — the unix read loop feeds single bytes,
			// while this table feeds whole chunks.
			var pb probeParser
			var doneB bool
			for i := 0; i < len(tc.input) && !doneB; i++ {
				_, doneB = pb.feed([]byte{tc.input[i]})
			}
			if pb.truecolor != tc.truecolor || doneB != tc.done {
				t.Errorf("byte-at-a-time: truecolor = %v done = %v, want %v/%v",
					pb.truecolor, doneB, tc.truecolor, tc.done)
			}
		})
	}
}

func TestProbeParserStopsAtBarrier(t *testing.T) {
	// feed must report consumption up to and including DA1's final
	// byte and nothing further: the read loop relies on this to
	// leave post-probe keystrokes in the kernel queue.
	var p probeParser
	input := []byte(da1 + "XYZ")
	n, done := p.feed(input)
	if !done {
		t.Fatal("done = false, want true")
	}
	if want := len(da1); n != want {
		t.Errorf("consumed %d bytes, want %d (stop exactly at DA1)", n, want)
	}
}

func TestApplyColorModeSetsProbeEligibility(t *testing.T) {
	resetColorState(t)
	resetProbeState(t)
	t.Setenv("NO_COLOR", "")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	SetStreams(nil, &bytes.Buffer{}, nil)
	t.Cleanup(ResetStreams)

	cases := []struct {
		mode string
		want bool
	}{
		{"auto", true},
		{"", true},
		{"truecolor", false},
		{"ansi", false},
		{"none", false},
	}
	for _, tc := range cases {
		ApplyColorMode(tc.mode)
		if autoColorMode != tc.want {
			t.Errorf("mode %q: autoColorMode = %v, want %v", tc.mode, autoColorMode, tc.want)
		}
	}

	// NO_COLOR remaps auto to none — detection never ran, so the
	// probe must not either.
	t.Setenv("NO_COLOR", "1")
	ApplyColorMode("auto")
	if autoColorMode {
		t.Error("auto under NO_COLOR: autoColorMode = true, want false")
	}
}

func TestProbeTruecolorUpgradeGates(t *testing.T) {
	resetColorState(t)
	resetProbeState(t)

	// Not auto mode: untouched even with an eligible profile.
	autoColorMode = false
	OutputProfile = colorprofile.ANSI256
	ProbeTruecolorUpgrade()
	if OutputProfile != colorprofile.ANSI256 {
		t.Errorf("forced mode: OutputProfile = %v, want ANSI256 untouched", OutputProfile)
	}

	// Auto mode but non-TTY streams (the test harness shape): the
	// probe must refuse to write query bytes into buffers.
	autoColorMode = true
	SetStreams(&bytes.Buffer{}, &bytes.Buffer{}, nil)
	t.Cleanup(ResetStreams)
	ProbeTruecolorUpgrade()
	if OutputProfile != colorprofile.ANSI256 {
		t.Errorf("non-TTY: OutputProfile = %v, want ANSI256 untouched", OutputProfile)
	}
	if out := defaultOutput.(*bytes.Buffer); out.Len() != 0 {
		t.Errorf("non-TTY: %d query bytes written to output, want none", out.Len())
	}

	// Profiles outside ANSI/ANSI256 never probe: nothing to refine
	// at TrueColor, no DCS evidence at Ascii and below.
	for _, prof := range []colorprofile.Profile{
		colorprofile.TrueColor, colorprofile.Ascii, colorprofile.NoTTY,
	} {
		OutputProfile = prof
		ProbeTruecolorUpgrade()
		if OutputProfile != prof {
			t.Errorf("profile %v: changed to %v, want untouched", prof, OutputProfile)
		}
	}
}

func TestApplyTruecolorUpgrade(t *testing.T) {
	resetColorState(t)
	// t.Setenv registers restoration of the invoking environment's
	// COLORTERM around the export below.
	t.Setenv("COLORTERM", "")

	applyDetectedProfile(colorprofile.ANSI256)
	applyTruecolorUpgrade()

	if OutputProfile != colorprofile.TrueColor {
		t.Errorf("OutputProfile = %v, want TrueColor", OutputProfile)
	}
	if Palette.Primary != defaultPalette().Primary {
		t.Errorf("Primary = %v, want full-fidelity default", Palette.Primary)
	}
	// The export makes fang's independent env detection agree with
	// the probe (see the FangColorScheme caveat).
	if got := os.Getenv("COLORTERM"); got != "truecolor" {
		t.Errorf("COLORTERM = %q, want %q", got, "truecolor")
	}
}
