package cli

import (
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
)

// The fang scheme must derive every color from the palette — issue
// #104 removed the last literals so help output downsamples with the
// run's pinned profile like every other surface.
func TestFangColorSchemeDerivesFromPalette(t *testing.T) {
	cs := FangColorScheme(nil)

	if cs.Title != ui.Palette.Primary {
		t.Errorf("Title = %v, want Palette.Primary", cs.Title)
	}
	if cs.ErrorHeader[0] != ui.Palette.ButtonFg {
		t.Errorf("ErrorHeader fg = %v, want Palette.ButtonFg", cs.ErrorHeader[0])
	}
	if cs.ErrorHeader[1] != ui.Palette.Error {
		t.Errorf("ErrorHeader bg = %v, want Palette.Error", cs.ErrorHeader[1])
	}
}
