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
			} else if ui.IsRaw() {
				// Errors must reach stderr even in raw mode where
				// Reporter methods are suppressed.
				ui.Error(err.Error())
			} else {
				ui.Fail(err.Error())
			}
			if !ui.IsRaw() {
				ui.EndTimeline()
			}
		}),
	}

	if err := fang.Execute(context.Background(), cmd, opts...); err != nil {
		os.Exit(1)
	}
}
