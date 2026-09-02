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
	"reflect"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// OutputProfile is the color profile every output pipeline encodes
// against. Set by ApplyColorMode before any rendering occurs; read
// freely afterward (single-goroutine init, same contract as
// Palette). The TrueColor initial value means "no conversion", which
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

// convertColor downsamples c through OutputProfile. Profiles at or
// below Ascii carry no colors at all, so the answer there is NoColor
// — not the input: this path handles colors synthesized at render
// time (lerpColors gradient steps), which exist even when the active
// palette is already colorless, and passing them through would leak
// truecolor SGR into no-color output. On quantizing profiles a nil
// Convert answer (only possible for a nil input) maps back to the
// input rather than letting a nil Foreground reach lipgloss.
func convertColor(c color.Color) color.Color {
	if OutputProfile <= colorprofile.Ascii {
		return lipgloss.NoColor{}
	}
	if cc := OutputProfile.Convert(c); cc != nil {
		return cc
	}
	return c
}

// convertPalette downsamples every color.Color field in p through
// prof, in place. Called only for profiles that quantize (ANSI256):
// basic-color palettes (ansi mode) and NoColor palettes never need
// it, and TrueColor passes through. The walk is reflective so a
// future palette field cannot be silently skipped — a hand-kept
// field list that missed one would leave that color truecolor while
// the rest quantized, quietly recreating the split this file exists
// to close.
func convertPalette(p *palette, prof colorprofile.Profile) {
	colorType := reflect.TypeOf((*color.Color)(nil)).Elem()
	v := reflect.ValueOf(p).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Type() != colorType || f.IsNil() {
			continue
		}
		if cc := prof.Convert(f.Interface().(color.Color)); cc != nil {
			f.Set(reflect.ValueOf(cc))
		}
	}
}
