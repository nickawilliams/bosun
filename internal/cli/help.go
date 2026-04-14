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
		ErrorHeader:    [2]color.Color{lipgloss.Color("#FFFDF5"), lipgloss.Color("#FF4672")},
		ErrorDetails:   ui.Palette.NormalFg,
	}
}
