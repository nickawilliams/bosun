//go:build linux

package ui

// Linux-only PTY tests for the startup probe. The gates and the full
// upgrade path need a real TTY on both streams, and Linux exposes a
// pty pair through /dev/ptmx with two stable ioctls — no external
// PTY dependency. On darwin the slave-name ioctl has no typed x/sys
// wrapper, so these paths are exercised there by the manual PTY
// matrix instead (issue #106's verification plan); CI runs Linux, so
// the lines stay covered where Codecov looks.

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"golang.org/x/sys/unix"
)

// openPTYPair allocates a pty and opens both ends. The slave is a
// real TTY — exactly what the probe's stdin/stdout gates require.
func openPTYPair(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no usable /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("ptsname: %v", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("opening pty slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

// clearProbeEnv removes the CI gate and COLORTERM from the test's
// environment (the invoking environment may set either), with
// restoration registered via t.Setenv.
func clearProbeEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"CI", "COLORTERM"} {
		t.Setenv(v, "")
		if err := os.Unsetenv(v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProbeTruecolorUpgradeTTYGates(t *testing.T) {
	resetColorState(t)
	resetProbeState(t)
	clearProbeEnv(t)
	_, slave := openPTYPair(t)

	autoColorMode = true
	OutputProfile = colorprofile.ANSI256

	// TTY stdin but non-TTY stdout: no probe, and in particular no
	// query bytes in the output stream.
	buf := &bytes.Buffer{}
	SetStreams(slave, buf, nil)
	t.Cleanup(ResetStreams)
	ProbeTruecolorUpgrade()
	if OutputProfile != colorprofile.ANSI256 {
		t.Errorf("non-TTY stdout: OutputProfile = %v, want ANSI256 untouched", OutputProfile)
	}
	if buf.Len() != 0 {
		t.Errorf("non-TTY stdout: %d query bytes written, want none", buf.Len())
	}

	// Both streams TTYs but a CI-shaped environment: never probe —
	// a stray query would pollute PTY-captured logs.
	t.Setenv("CI", "true")
	SetStreams(slave, slave, nil)
	ProbeTruecolorUpgrade()
	if OutputProfile != colorprofile.ANSI256 {
		t.Errorf("CI: OutputProfile = %v, want ANSI256 untouched", OutputProfile)
	}
}

func TestProbeTruecolorUpgradeEndToEnd(t *testing.T) {
	resetColorState(t)
	resetProbeState(t)
	clearProbeEnv(t)
	master, slave := openPTYPair(t)

	autoColorMode = true
	OutputProfile = colorprofile.ANSI256
	Palette = ansiPalette()
	SetStreams(slave, slave, nil)
	t.Cleanup(ResetStreams)

	// Play the terminal: read the master until the DA1 query's final
	// byte arrives (proving the probe actually sent its queries),
	// then answer with a Tc hit and the DA1 barrier.
	go func() {
		buf := make([]byte, 1)
		var seen []byte
		for {
			n, err := master.Read(buf)
			if err != nil || n == 0 {
				return
			}
			seen = append(seen, buf[0])
			if bytes.HasSuffix(seen, []byte("\x1b[c")) {
				_, _ = master.WriteString("\x1bP1+r5463\x1b\\" + da1)
				return
			}
		}
	}()

	ProbeTruecolorUpgrade()

	if OutputProfile != colorprofile.TrueColor {
		t.Errorf("OutputProfile = %v, want TrueColor after a Tc answer", OutputProfile)
	}
	if Palette.Primary != defaultPalette().Primary {
		t.Errorf("Primary = %v, want full-fidelity default", Palette.Primary)
	}
	if got := os.Getenv("COLORTERM"); got != "truecolor" {
		t.Errorf("COLORTERM = %q, want %q exported on upgrade", got, "truecolor")
	}
}

func TestProbeTruecolorUpgradeMuteTerminal(t *testing.T) {
	resetColorState(t)
	resetProbeState(t)
	clearProbeEnv(t)
	_, slave := openPTYPair(t)

	autoColorMode = true
	OutputProfile = colorprofile.ANSI256
	SetStreams(slave, slave, nil)
	t.Cleanup(ResetStreams)

	// A terminal that never answers: the probe must ride out its
	// timeout and leave the env-detected profile untouched.
	ProbeTruecolorUpgrade()
	if OutputProfile != colorprofile.ANSI256 {
		t.Errorf("mute terminal: OutputProfile = %v, want ANSI256 untouched", OutputProfile)
	}
}
