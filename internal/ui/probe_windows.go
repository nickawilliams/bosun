//go:build windows

package ui

import "os"

// probeTerminal reports no answer on Windows, skipping the probe.
// The classic console has no XTGETTCAP, and Windows Terminal — which
// does render truecolor — is already recognized by colorprofile's
// env detection, so there is no under-advertised case for the probe
// to close. Skipping keeps the raw-mode dance unix-only.
func probeTerminal(_, _ *os.File) bool { return false }
