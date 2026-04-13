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
		if errors.Is(err, cli.ErrCancelled) {
			ui.NewCard(ui.CardSkipped, "Cancelled").Print()
			os.Exit(0)
		}
		ui.Error("%s", err)
		os.Exit(1)
	}
}
