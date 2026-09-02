package ui

// Color profile resolution (issue #104).
//
// Bosun's UI is an append-only scrollback timeline: committed lines
// can never be repainted, so "how many colors does this terminal
// get" must be a single decision made before the first styled byte
// and held for the whole run. A profile that changes mid-run — like
// BubbleTea's opt-in XTGETTCAP upgrade — would freeze earlier cards
// at one fidelity and later cards at another.
//
// Rendered strings reach the terminal through three sinks with
// different downsampling behavior: plain fmt.Print paths (no
// downsampling), BubbleTea's managed frame (downsampled to the
// program's profile), and scrollback commits via Program.Println
// (written verbatim — the renderer skips its profile pass; see
// charmbracelet/bubbletea#1709). Rather than filtering all three
// sinks, the palette itself is converted to the resolved profile
// once at startup: every style then emits already-downsampled
// colors, so all sinks produce identical bytes and BubbleTea's own
// frame downsampling becomes a no-op. The BubbleTea programs are
// still pinned to the same profile (TeaColorProfile) so the frame
// pass can never disagree, even if detection inside bubbletea and
// here were to diverge.

import (
	"image/color"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// OutputProfile is the color profile every output pipeline encodes
// against. Set by ApplyColorMode before any rendering occurs; read
// freely afterward (single-goroutine init, same contract as
// Palette). The TrueColor zero state means "no conversion", which
// keeps pre-ApplyColorMode rendering (unit tests that touch styles
// directly) at full fidelity.
var OutputProfile = colorprofile.TrueColor

// resolveProfile detects the terminal's color capability for the
// auto color mode. Non-TTY output (pipes, redirects, injected test
// buffers) resolves to TrueColor — i.e. pass colors through
// unmodified. The reporter selection in bootstrap already decides
// what non-TTY consumers see (plain/raw), and the styled paths that
// deliberately render into pipes keep today's full-fidelity bytes;
// the writer must not re-decide what that layer already decided.
func resolveProfile() colorprofile.Profile {
	if !IsTerminalWriter(defaultOutput) {
		return colorprofile.TrueColor
	}
	return colorprofile.Detect(defaultOutput, os.Environ())
}

// TeaColorProfile returns the ProgramOption that pins a BubbleTea
// program to the resolved profile. Every tea.NewProgram in the
// package (and huh forms via the cli layer) passes it, so the
// managed frame and the palette agree on encoding by construction.
func TeaColorProfile() tea.ProgramOption {
	return tea.WithColorProfile(OutputProfile)
}

// convertColor downsamples c through OutputProfile, preserving c
// when conversion is a no-op or undefined. Profiles at or below
// Ascii convert every color to nil (colorprofile's "no color"
// answer); callers on those profiles already hold the NoColor
// palette, so nil is mapped back to the input rather than letting a
// nil Foreground reach lipgloss.
func convertColor(c color.Color) color.Color {
	if cc := OutputProfile.Convert(c); cc != nil {
		return cc
	}
	return c
}

// convertPalette downsamples every color in p through prof, in
// place. Called only for profiles that quantize (ANSI256): basic-
// color palettes (ansi mode) and NoColor palettes never need it,
// and TrueColor passes through.
func convertPalette(p *palette, prof colorprofile.Profile) {
	fields := []*color.Color{
		&p.Primary, &p.Secondary, &p.Brand, &p.LogoTop, &p.LogoBottom,
		&p.Accent, &p.Info, &p.Success, &p.Error, &p.Warning,
		&p.Muted, &p.NormalFg,
		&p.RoleOpen, &p.RoleDone, &p.RoleClosed, &p.RoleAttention,
		&p.RoleInFlight, &p.RoleNeutral, &p.Keyword,
		&p.Recessed, &p.Border, &p.Subtle, &p.ButtonFg,
	}
	for _, f := range fields {
		if *f == nil {
			continue
		}
		if cc := prof.Convert(*f); cc != nil {
			*f = cc
		}
	}
}
