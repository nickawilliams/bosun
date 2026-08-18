package cli

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/audio"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func printCaptainArt() {
	// Raw rows, not cards — so the timeline's open/continuing swap
	// doesn't fire on its own. Restore the spine on whatever is above
	// before painting over the rows below it.
	ui.FinalizeOpenCard()
	conn := lipgloss.NewStyle().Foreground(ui.Palette.Recessed).Render(ui.BoxVertical)
	style := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	for _, line := range captainArt {
		fmt.Printf(" %s  %s\n", conn, style.Render(line))
	}
}

var captainArt = []string{
	"",
	"            @@@@@@@           @@    @@           @@@@@@@",
	"         @@@@@@@@@@@@    &@@@@@@@  @@@@@@@@   @@@@@@@@@@@@",
	"        @@@@@   @@@@@ @@@@@@@@@@    @@@@@@@@@@ @@@@@  @@@@@",
	"        @@@@     @@@@@@@@@@@@@@@    @@@@@@@@@@@@@@@     @@@@",
	"        @@@@@@ @@@@@@@@@@@@@@@@      @@@@@@@@@@@@@@@@  @@@@@",
	"          @@@@@@@@@@@@@@@@@@@@        @@@@@@@@@@@@@@@@@@@@@",
	"            @@@@@@@@@@@@@@@@            @@@@@@@@@@@@@@@@",
	"               @@@@@@@@@@@               @@@@@@@@@@@",
	"             @@@@@@@@@@@@@@@@          @@@@@@@@@@@@@@@@",
	"           @@@@@@@@@@@ @@@@@@@@@    @@@@@@@@@ @@@@@@@@@@@@",
	"          @@@@@@@@@@     @@@@@@@@@@@@@@@@@      @@@@@@@@@@",
	"         @@@@@@@            @@@@@@@@@@@@            @@@@@@@@",
	"         @@@@                 @@@@@@@@@@                @@@@",
	"                                @@@@@@@@",
	"              @               @@@@@@@@@@@              @",
	"          @@@@             @@@@@@@@  @@@@@@@@          @@@@@",
	"        @@@@@@@@         @@@@@@@@     @@@@@@@@       @@@@@@@@",
	"      @@@@@@@@@@       @@@@@@@@         @@@@@@@@    @@@@@@@@@@",
	"     @@@@@@@@@@@     @@@@@@@@             @@@@@@@@  @@@@@@@@@@@",
	"        @@@@@@@    @@@@@@@@                 @@@@@@@@@  @@@@@@@",
	"        @@@@@@@@@@@@@@@@@@                   @@@@@@@@@@@@@@@@@",
	"         @@@@@@@@@@@@@@@@                      @@@@@@@@@@@@@@",
	"          @@@@@@@@@@@@@@                        @@@@@@@@@@@@@@",
	"           @@@@@@@@@@@@  @@@@@@@@@  @@@@@@@@@@  @@@@@@@@@@@@",
	"           @@@@@@@@@@@@@@@@@@@@@@@  @@@@@@@@@@@@@@@@@@@@@@@@",
	"                 @@@@@@@@@@@@@@@@@   @@@@@@@@@@@@@@@@@",
	"                     @@@@@@@@@@@       @@@@@@@@@@@",
	"                          @@@@@@         @@@@@@",
	"                           @@               @@",
}

func newCaptainCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "captain on deck",
		Hidden: true,
		Annotations: map[string]string{
			headerAnnotationTitle:       "Captain On Deck!",
			headerAnnotationHideOnError: "true",
		},
		Args: func(cmd *cobra.Command, args []string) error {
			expected := []string{"on", "deck"}
			// Validate provided args positionally so each wrong word
			// surfaces a specific "no orders for X" message rather than
			// a generic count error. Missing args fall through to the
			// count check below.
			for i, got := range args {
				if i >= len(expected) || got != expected[i] {
					return fmt.Errorf("captain has no orders for %q", got)
				}
			}
			if missing := len(expected) - len(args); missing > 0 {
				word := "orders"
				if missing == 1 {
					word = "order"
				}
				return fmt.Errorf("captain awaits %d more %s", missing, word)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			printCaptainArt()
			audio.Play()
			return nil
		},
	}
}
