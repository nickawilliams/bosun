package cli

import (
	"github.com/nickawilliams/bosun/internal/audio"
	"github.com/spf13/cobra"
)

func newCaptainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "captain",
		Hidden: true,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "on",
		Short: "on deck",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && args[0] == "deck" {
				audio.Play()
			}
			return nil
		},
		Args: cobra.ExactArgs(1),
	})

	return cmd
}
