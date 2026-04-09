package main

import (
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
		ui.Error("%s", err)
		os.Exit(1)
	}
}
