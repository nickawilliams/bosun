package ui

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// resetColorState restores the package's color globals after a test
// that swaps palettes or profiles. Mirrors the single-goroutine init
// contract: tests that mutate this state must not run in parallel.
func resetColorState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		OutputProfile = colorprofile.TrueColor
		Palette = defaultPalette()
		rebuildStyles()
	})
}

func TestApplyDetectedProfile(t *testing.T) {
	resetColorState(t)

	t.Run("truecolor passes the palette through", func(t *testing.T) {
		applyDetectedProfile(colorprofile.TrueColor)
		if OutputProfile != colorprofile.TrueColor {
			t.Fatalf("OutputProfile = %v, want TrueColor", OutputProfile)
		}
		if Palette.Primary != defaultPalette().Primary {
			t.Errorf("Primary = %v, want unconverted default", Palette.Primary)
		}
	})

	t.Run("ansi256 quantizes every palette color", func(t *testing.T) {
		applyDetectedProfile(colorprofile.ANSI256)
		if OutputProfile != colorprofile.ANSI256 {
			t.Fatalf("OutputProfile = %v, want ANSI256", OutputProfile)
		}
		raw := defaultPalette()
		want := colorprofile.ANSI256.Convert(raw.Primary)
		if Palette.Primary != want {
			t.Errorf("Primary = %v, want %v (256-quantized)", Palette.Primary, want)
		}
		// The grays are the standard xterm grayscale values, so
		// quantization must round-trip them onto the 256 ramp — the
		// same bytes the old index-named grays produced.
		if got := colorprofile.ANSI256.Convert(raw.Muted); Palette.Muted != got {
			t.Errorf("Muted = %v, want %v", Palette.Muted, got)
		}
		// Role aliases were populated before conversion; they must
		// track their severity source through it.
		if Palette.RoleDone != Palette.Primary {
			t.Errorf("RoleDone = %v, want Primary %v", Palette.RoleDone, Palette.Primary)
		}
		// Completeness: every color field of the palette — present
		// and future — must be quantized. An unconverted straggler
		// would render truecolor beside 256-color siblings, the
		// exact split issue #104 closed.
		colorType := reflect.TypeOf((*color.Color)(nil)).Elem()
		v := reflect.ValueOf(&Palette).Elem()
		for i := range v.NumField() {
			f := v.Field(i)
			if f.Type() != colorType || f.IsNil() {
				continue
			}
			c := f.Interface().(color.Color)
			if quantized := colorprofile.ANSI256.Convert(c); c != quantized {
				t.Errorf("palette field %s = %v escaped quantization (want %v)",
					v.Type().Field(i).Name, c, quantized)
			}
		}
	})

	t.Run("ansi uses the hand-tuned palette", func(t *testing.T) {
		applyDetectedProfile(colorprofile.ANSI)
		if OutputProfile != colorprofile.ANSI {
			t.Fatalf("OutputProfile = %v, want ANSI", OutputProfile)
		}
		if Palette.Primary != ansiPalette().Primary {
			t.Errorf("Primary = %v, want ansiPalette Primary", Palette.Primary)
		}
	})

	t.Run("ascii and below fall back to no color", func(t *testing.T) {
		for _, p := range []colorprofile.Profile{colorprofile.Ascii, colorprofile.NoTTY} {
			applyDetectedProfile(p)
			if OutputProfile != colorprofile.Ascii {
				t.Errorf("detected %v: OutputProfile = %v, want Ascii", p, OutputProfile)
			}
			if Palette.Primary != (lipgloss.NoColor{}) {
				t.Errorf("detected %v: Primary = %v, want NoColor", p, Palette.Primary)
			}
		}
	})
}

func TestApplyColorModeProfiles(t *testing.T) {
	resetColorState(t)
	// The invoking environment may export NO_COLOR (it remaps the
	// unset and auto modes); Setenv registers restoration, Unsetenv
	// clears it for the assertions below.
	t.Setenv("NO_COLOR", "")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		mode string
		want colorprofile.Profile
	}{
		{"truecolor", colorprofile.TrueColor},
		{"ansi", colorprofile.ANSI},
		{"none", colorprofile.Ascii},
		// Auto resolves through detection; with the test buffer
		// output below (non-TTY) that is the TrueColor passthrough,
		// preserving full-fidelity bytes for piped/captured output.
		{"auto", colorprofile.TrueColor},
		{"", colorprofile.TrueColor},
	}
	// Non-TTY output stream, as bootstrap wires for captured runs.
	SetStreams(nil, &bytes.Buffer{}, nil)
	t.Cleanup(ResetStreams)

	for _, tc := range cases {
		ApplyColorMode(tc.mode)
		if OutputProfile != tc.want {
			t.Errorf("mode %q: OutputProfile = %v, want %v", tc.mode, OutputProfile, tc.want)
		}
	}
}

func TestApplyColorModeNoColorEnv(t *testing.T) {
	resetColorState(t)
	t.Setenv("NO_COLOR", "1")

	// Unset and explicit auto both mean "respect the environment",
	// so NO_COLOR wins for either — including on non-TTY output,
	// where auto's detection short-circuit would never consult it.
	for _, mode := range []string{"", "auto"} {
		ApplyColorMode(mode)
		if OutputProfile != colorprofile.Ascii {
			t.Errorf("mode %q under NO_COLOR: OutputProfile = %v, want Ascii", mode, OutputProfile)
		}
		if Palette.Primary != (lipgloss.NoColor{}) {
			t.Errorf("mode %q under NO_COLOR: Primary = %v, want NoColor", mode, Palette.Primary)
		}
	}

	// Explicit truecolor: user config wins over the env var.
	ApplyColorMode("truecolor")
	if OutputProfile != colorprofile.TrueColor {
		t.Errorf("explicit truecolor under NO_COLOR: OutputProfile = %v, want TrueColor", OutputProfile)
	}
}

func TestLerpColorsConvertThroughProfile(t *testing.T) {
	resetColorState(t)
	OutputProfile = colorprofile.ANSI256

	a := lipgloss.Color("#7571F9")
	b := lipgloss.Color("#9997CC")
	for i, c := range lerpColors(a, b, 4) {
		if want := colorprofile.ANSI256.Convert(c); c != want {
			t.Errorf("step %d = %v, not quantized (want %v)", i, c, want)
		}
	}
	// The n<=1 short-circuit converts too.
	if got := lerpColors(a, b, 1)[0]; got != colorprofile.ANSI256.Convert(a) {
		t.Errorf("single = %v, want quantized endpoint", got)
	}
}

func TestConvertPaletteSkipsNilFields(t *testing.T) {
	resetColorState(t)

	p := defaultPalette()
	p.Border = nil // a constructor that forgot a field must not panic Convert
	convertPalette(&p, colorprofile.ANSI256)
	if p.Border != nil {
		t.Errorf("Border = %v, want nil preserved", p.Border)
	}
	if want := colorprofile.ANSI256.Convert(defaultPalette().Primary); p.Primary != want {
		t.Errorf("Primary = %v, want %v — nil field must not stop conversion", p.Primary, want)
	}
}

func TestConvertColorStripsBelowANSI(t *testing.T) {
	resetColorState(t)
	OutputProfile = colorprofile.Ascii

	// Colors synthesized at render time (lerp gradient steps) exist
	// even when the palette is colorless; below ANSI they must strip
	// to NoColor, never pass through as truecolor SGR.
	c := lipgloss.Color("#7571F9")
	if got := convertColor(c); got != (lipgloss.NoColor{}) {
		t.Errorf("Ascii profile: convertColor = %v, want NoColor", got)
	}
}

// TestNoColorLiteralsOutsideTheme enforces the single-source-of-truth
// rule from issue #104: every color in the application derives from
// the palette in theme.go, so profile conversion provably covers all
// of them. A lipgloss.Color literal anywhere else escapes the pinned
// profile and re-introduces the split this issue closed.
func TestNoColorLiteralsOutsideTheme(t *testing.T) {
	root := repoRoot(t)
	literal := regexp.MustCompile(`lipgloss\.Color\(`)

	var offenders []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip nested checkouts and build output.
				switch d.Name() {
				case ".git", ".claude", ".out", ".workspaces", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Base(path) == "theme.go" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if literal.Match(src) {
				rel, _ := filepath.Rel(root, path)
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("lipgloss.Color literals outside internal/ui/theme.go (route through ui.Palette instead):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// repoRoot walks up from the package directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above package directory")
		}
		dir = parent
	}
}
