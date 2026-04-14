package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	versionStr := fmt.Sprintf("%s (%s, %s)", version, commit, date)
	cmd := cli.NewRootCmd(versionStr)
	if err := cmd.Execute(); err != nil {
		ui.BreakTimeline()
		if errors.Is(err, cli.ErrCancelled) {
			ui.NewCard(ui.CardSkipped, "User cancelled").Print()
			ui.EndTimeline()
			os.Exit(0)
		}
		ui.Error("%s", err)
		ui.EndTimeline()
		os.Exit(1)
	}
}
