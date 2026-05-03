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
	ui.AppVersion = version
	cmd := cli.NewRootCmd()

	opts := []fang.Option{
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(cli.FangColorScheme),
		fang.WithoutManpage(),
		fang.WithErrorHandler(func(_ io.Writer, _ fang.Styles, err error) {
			if errors.Is(err, cli.ErrCancelled) {
				ui.NewCard(ui.CardSkipped, "user cancelled").Print()
				if !ui.IsRaw() {
					ui.EndTimeline()
				}
				return
			}
			if ui.IsRaw() {
				ui.Error(err.Error())
			} else {
				ui.Fail(err.Error())
				ui.EndTimeline()
			}
			os.Exit(1)
		}),
	}

	if err := fang.Execute(context.Background(), cmd, opts...); err != nil {
		os.Exit(1)
	}
}
