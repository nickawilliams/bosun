package cli

import (
	"image/color"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// FangColorScheme returns a fang color scheme that matches the bosun
// palette. The isLight parameter from fang's LightDarkFunc is unused
// since bosun manages its own palette via color_mode config.
func FangColorScheme(_ lipgloss.LightDarkFunc) fang.ColorScheme {
	return fang.ColorScheme{
		Base:           ui.Palette.NormalFg,
		Title:          ui.Palette.Primary,
		Description:    ui.Palette.Muted,
		Help:           ui.Palette.Muted,
		Dash:           ui.Palette.Recessed,
		Codeblock:      ui.Palette.Recessed,
		Program:        ui.Palette.NormalFg,
		Command:        ui.Palette.Accent,
		Argument:       ui.Palette.NormalFg,
		DimmedArgument: ui.Palette.Muted,
		QuotedString:   ui.Palette.Success,
		Comment:        ui.Palette.Muted,
		Flag:           ui.Palette.Accent,
		FlagDefault:    ui.Palette.Muted,
		// Foreground on background: the button foreground on the
		// palette error red. Derived from the palette (not literals)
		// so help colors quantize with the rest of the app in auto
		// mode. Caveat: fang wraps its output in its own
		// colorprofile writer with its own env detection and exposes
		// no way to inject bosun's pinned profile, so under an
		// explicit truecolor override in a terminal that doesn't
		// advertise it, help alone still downsamples. Fixing that
		// needs fang to accept a profile; see issue #104. The
		// startup probe (issue #106) narrows the auto-mode case:
		// a probe-confirmed upgrade exports COLORTERM=truecolor,
		// which fang's own detection picks up — but only on
		// invocations that bootstrap eagerly (usage/error
		// rendering, bare `bosun`); help-like argv skips Bootstrap
		// and never probes, so explicit --help stays env-detected.
		ErrorHeader:  [2]color.Color{ui.Palette.ButtonFg, ui.Palette.Error},
		ErrorDetails: ui.Palette.NormalFg,
	}
}
