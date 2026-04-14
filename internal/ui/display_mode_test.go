package ui

import "testing"

func TestApplyDisplayMode(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact })

	tests := []struct {
		input string
		want  DisplayMode
	}{
		{"", DisplayCompact},
		{"compact", DisplayCompact},
		{"comfy", DisplayComfy},
		{"verbose", DisplayVerbose},
		{"unknown", DisplayCompact},
	}
	for _, tt := range tests {
		t.Run("input="+tt.input, func(t *testing.T) {
			ApplyDisplayMode(tt.input)
			if displayMode != tt.want {
				t.Errorf("ApplyDisplayMode(%q): got %d, want %d", tt.input, displayMode, tt.want)
			}
		})
	}
}

func TestApplyDisplayMode_CaseInsensitive(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact })

	for _, input := range []string{"Comfy", "COMFY", " comfy "} {
		ApplyDisplayMode(input)
		if displayMode != DisplayComfy {
			t.Errorf("ApplyDisplayMode(%q): got %d, want DisplayComfy", input, displayMode)
		}
	}
}

func TestIsComfy(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact })

	tests := []struct {
		mode DisplayMode
		want bool
	}{
		{DisplayCompact, false},
		{DisplayComfy, true},
		{DisplayVerbose, true},
	}
	for _, tt := range tests {
		displayMode = tt.mode
		if got := IsComfy(); got != tt.want {
			t.Errorf("IsComfy() with mode %d: got %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestDisplayPadding(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact })

	displayMode = DisplayCompact
	if got := displayPadding(); got != "" {
		t.Errorf("displayPadding() compact: got %q, want %q", got, "")
	}

	displayMode = DisplayComfy
	if got := displayPadding(); got != "\n" {
		t.Errorf("displayPadding() comfortable: got %q, want %q", got, "\n")
	}

	displayMode = DisplayVerbose
	if got := displayPadding(); got != "\n" {
		t.Errorf("displayPadding() verbose: got %q, want %q", got, "\n")
	}
}

func TestComfyPrefix_Compact(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact; comfyBreak = false })

	displayMode = DisplayCompact
	comfyBreak = true
	if got := comfyPrefix(); got != "" {
		t.Errorf("comfyPrefix() in compact mode: got %q, want %q", got, "")
	}
	if comfyBreak {
		t.Error("comfyPrefix() should clear comfyBreak even in compact mode")
	}
}

func TestComfyPrefix_Comfy(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact; comfyBreak = false })

	displayMode = DisplayComfy

	// Without comfyBreak set, returns empty.
	comfyBreak = false
	if got := comfyPrefix(); got != "" {
		t.Errorf("comfyPrefix() without break: got %q, want %q", got, "")
	}

	// With comfyBreak set, returns connector line.
	comfyBreak = true
	got := comfyPrefix()
	if got == "" {
		t.Fatal("comfyPrefix() with break: got empty, want connector line")
	}
	if got[len(got)-1] != '\n' {
		t.Error("comfyPrefix() should end with newline")
	}
	if comfyBreak {
		t.Error("comfyPrefix() should clear comfyBreak after emitting")
	}
}

func TestCardRender_UnchangedByDisplayMode(t *testing.T) {
	t.Cleanup(func() { displayMode = DisplayCompact })

	card := NewCard(CardSuccess, "test title")

	displayMode = DisplayCompact
	compact := card.Render()

	displayMode = DisplayComfy
	comfortable := card.Render()

	if compact != comfortable {
		t.Error("Card.Render() output should be identical regardless of display mode")
	}
}
