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

	// Resolve the target command from os.Args and pre-parse its
	// flags so Bootstrap can honor --project / --output before
	// loading config and picking the renderer. cmd.Find only walks
	// the command tree (no validators fire); ParseFlags' error is
	// ignored on purpose — anything pflag rejects here will be
	// rejected again by cobra/fang and flow to HandleError, which by
	// then has a fully-configured UI to render it.
	target, remaining, _ := cmd.Find(os.Args[1:])
	if target != nil {
		_ = target.ParseFlags(remaining)
	}
	if err := cli.Bootstrap(target); err != nil {
		cli.HandleError(err)
		os.Exit(1)
	}

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
