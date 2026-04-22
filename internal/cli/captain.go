package cli

import (
	"github.com/nickawilliams/bosun/internal/audio"
	"github.com/spf13/cobra"
)

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
				audio.Play()
			}
			return nil
		},
	}
}
