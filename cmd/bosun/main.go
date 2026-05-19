package main

import (
	"context"
	"io"
	"os"

	"charm.land/fang/v2"
	"github.com/nickawilliams/bosun/internal/cli"
)

var version = "dev"

func main() {
	cmd := cli.NewRootCmd(version)

	opts := []fang.Option{
		fang.WithVersion(version),
		fang.WithColorSchemeFunc(cli.FangColorScheme),
		fang.WithoutManpage(),
		fang.WithErrorHandler(func(_ io.Writer, _ fang.Styles, err error) {
			cli.HandleError(err)
		}),
	}

	if err := fang.Execute(context.Background(), cmd, opts...); err != nil {
		os.Exit(1)
	}
}
