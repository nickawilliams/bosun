package cli

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/audio"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func printCaptainArt() {
	conn := lipgloss.NewStyle().Foreground(ui.Palette.Recessed).Render("│")
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
			headerAnnotationTitle: "Captain On Deck!",
		},
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "on" && args[1] == "deck" {
				rootCard(cmd).Print()
				printCaptainArt()
				audio.Play()
			}
			return nil
		},
	}
}
