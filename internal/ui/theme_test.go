package ui

import (
	"image/color"
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestLerpColors(t *testing.T) {
	a := lipgloss.Color("#000000")
	b := lipgloss.Color("#ffffff")

	t.Run("single element returns first color", func(t *testing.T) {
		colors := lerpColors(a, b, 1)
		if len(colors) != 1 {
			t.Fatalf("len = %d, want 1", len(colors))
		}
	})

	t.Run("two elements returns endpoints", func(t *testing.T) {
		colors := lerpColors(a, b, 2)
		if len(colors) != 2 {
			t.Fatalf("len = %d, want 2", len(colors))
		}
		assertColorNear(t, colors[0], 0, 0, 0)
		assertColorNear(t, colors[1], 255, 255, 255)
	})

	t.Run("midpoint is interpolated", func(t *testing.T) {
		colors := lerpColors(a, b, 3)
		if len(colors) != 3 {
			t.Fatalf("len = %d, want 3", len(colors))
		}
		assertColorNear(t, colors[1], 127, 127, 127)
	})

	t.Run("zero elements returns first color", func(t *testing.T) {
		colors := lerpColors(a, b, 0)
		if len(colors) != 1 {
			t.Fatalf("len = %d, want 1", len(colors))
		}
	})
}

func assertColorNear(t *testing.T, c color.Color, wantR, wantG, wantB uint8) {
	t.Helper()
	r, g, b, _ := c.RGBA()
	gotR, gotG, gotB := uint8(r>>8), uint8(g>>8), uint8(b>>8)
	const tolerance = 3
	if diff(gotR, wantR) > tolerance || diff(gotG, wantG) > tolerance || diff(gotB, wantB) > tolerance {
		t.Errorf("color = (%d,%d,%d), want near (%d,%d,%d)", gotR, gotG, gotB, wantR, wantG, wantB)
	}
}

func diff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// Every palette constructor must end with applyRoleAliases. Forgetting it
// leaves all seven Role* fields nil and the state grammar renders colorless
// in that one color mode — no panic, no build error, just wrong output. The
// nil check catches that in any constructor.
//
// Each role is also compared against its source, which catches a miswiring
// (RoleOpen aliased to Error, say) that a nil check cannot. Only the colored
// palettes can detect that: noColorPalette sets every field to the same
// lipgloss.NoColor{}, so equality there holds no matter how it is wired.
func TestPaletteConstructorsApplyRoleAliases(t *testing.T) {
	constructors := []struct {
		name string
		fn   func() palette
	}{
		{"default", defaultPalette},
		{"ansi", ansiPalette},
		{"none", noColorPalette},
	}

	for _, tc := range constructors {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.fn()
			roles := []struct {
				name       string
				got, want  color.Color
				sourceName string
			}{
				{"RoleOpen", p.RoleOpen, p.Success, "Success"},
				{"RoleDone", p.RoleDone, p.Primary, "Primary"},
				{"RoleClosed", p.RoleClosed, p.Error, "Error"},
				{"RoleAttention", p.RoleAttention, p.Warning, "Warning"},
				{"RoleInFlight", p.RoleInFlight, p.Info, "Info"},
				{"RoleNeutral", p.RoleNeutral, p.Muted, "Muted"},
				{"Keyword", p.Keyword, p.Primary, "Primary"},
			}
			for _, r := range roles {
				if r.got == nil {
					t.Errorf("%s is nil — did %sPalette skip applyRoleAliases?", r.name, tc.name)
					continue
				}
				if r.got != r.want {
					t.Errorf("%s = %v, want %s (%v)", r.name, r.got, r.sourceName, r.want)
				}
			}
		})
	}
}

// The symbol counterpart to the role-alias check above. Forgetting
// applyUnicodeSymbols leaves every symbol field "", which renders as
// a missing glyph in that one color mode — no panic, no build error,
// just a blank gutter. Symbols are orthogonal to color, so all three
// constructors must also agree: a glyph mode is a separate axis, and
// picking "none" for color must never silently change the glyphs.
func TestPaletteConstructorsApplyUnicodeSymbols(t *testing.T) {
	symbolsOf := func(p palette) map[string]string {
		return map[string]string{
			"Check":     p.Check,
			"Cross":     p.Cross,
			"Active":    p.Active,
			"Attention": p.Attention,
			"Waiting":   p.Waiting,
			"Unknown":   p.Unknown,
			"Pending":   p.Pending,
			"Inactive":  p.Inactive,
			"Arrow":     p.Arrow,
			"Bullet":    p.Bullet,
			"Dot":       p.Dot,
		}
	}

	want := symbolsOf(defaultPalette())

	// Guard the guard: if a field is added to the struct but not to
	// symbolsOf, this test would silently stop covering it.
	if len(want) != countSymbolFields(t) {
		t.Fatalf("symbolsOf covers %d fields, but palette declares %d string symbol fields — update the test",
			len(want), countSymbolFields(t))
	}

	for name, glyph := range want {
		if glyph == "" {
			t.Errorf("default palette: %s is empty — did defaultPalette skip applyUnicodeSymbols?", name)
		}
	}

	for _, tc := range []struct {
		name string
		fn   func() palette
	}{
		{"ansi", ansiPalette},
		{"none", noColorPalette},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, glyph := range symbolsOf(tc.fn()) {
				if glyph != want[name] {
					t.Errorf("%s = %q, want %q — symbols must not vary by color mode",
						name, glyph, want[name])
				}
			}
		})
	}
}

// The concrete vocabulary, pinned. Everywhere else in the codebase a
// literal ✓ is a bug — this is the one place the characters are
// spelled out, so that repointing a call site at the wrong field
// (Palette.Active where Palette.Check was meant) still changes what
// the user sees, and a test somewhere fails. Update deliberately.
func TestDefaultSymbolVocabulary(t *testing.T) {
	p := defaultPalette()
	for _, tc := range []struct {
		name      string
		got, want string
	}{
		{"Check", p.Check, "✓"},
		{"Cross", p.Cross, "✗"},
		{"Active", p.Active, "●"},
		{"Attention", p.Attention, "▲"},
		{"Waiting", p.Waiting, "⧗"},
		{"Unknown", p.Unknown, "?"},
		{"Pending", p.Pending, "◦"},
		{"Inactive", p.Inactive, "○"},
		{"Arrow", p.Arrow, "→"},
		{"Bullet", p.Bullet, "•"},
		{"Dot", p.Dot, "·"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// countSymbolFields reports how many string-typed fields the palette
// struct declares. Every one of them is a symbol — the struct's other
// fields are all color.Color.
func countSymbolFields(t *testing.T) int {
	t.Helper()
	ty := reflect.TypeOf(palette{})
	n := 0
	for i := range ty.NumField() {
		if ty.Field(i).Type.Kind() == reflect.String {
			n++
		}
	}
	return n
}

func TestSpacerPrefix(t *testing.T) {
	t.Cleanup(func() { needsSpacer = false })

	// Initially false — first call returns empty and arms.
	needsSpacer = false
	if got := spacerPrefix(); got != "" {
		t.Errorf("first call: got %q, want empty", got)
	}

	// Now armed — second call returns the connector line.
	if got := spacerPrefix(); got == "" {
		t.Error("second call: got empty, want connector line")
	}

	// Auto-rearmed — third call also returns connector.
	if got := spacerPrefix(); got == "" {
		t.Error("third call: got empty, want connector line")
	}

	// ClearSpacer suppresses.
	ClearSpacer()
	if got := spacerPrefix(); got != "" {
		t.Errorf("after ClearSpacer: got %q, want empty", got)
	}
}
