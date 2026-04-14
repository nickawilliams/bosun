package main

import (
	"context"
	"errors"
	"io"
	"os"

	"charm.land/fang/v2"
	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	cmd := cli.NewRootCmd()

	err := fang.Execute(context.Background(), cmd,
		fang.WithVersion(version),
		fang.WithCommit(commit),
		fang.WithColorSchemeFunc(cli.FangColorScheme),
		fang.WithoutManpage(),
		fang.WithErrorHandler(func(_ io.Writer, _ fang.Styles, err error) {
			ui.BreakTimeline()
			if errors.Is(err, cli.ErrCancelled) {
				ui.NewCard(ui.CardSkipped, "User cancelled").Print()
				ui.EndTimeline()
				return
			}
			ui.Error("%s", err)
			ui.EndTimeline()
		}),
	)
	if err != nil {
		os.Exit(1)
	}
}
