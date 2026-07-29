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

	// Resolve the target command from os.Args and read the bootstrap
	// flags (--project / --output) so Bootstrap can honor them before
	// loading config and picking the renderer. cmd.Find only walks
	// the command tree (no validators fire). The read goes through a
	// throwaway FlagSet — parsing the target's real FlagSet here
	// would make cobra/fang's later authoritative parse the *second*
	// parse, and pflag appends slice values on a re-parse, doubling
	// every --service / --reviewer style flag. Malformed flags are
	// ignored on purpose: cobra/fang rejects them again and flows to
	// HandleError, which by then has a fully-configured UI.
	target, remaining, _ := cmd.Find(os.Args[1:])
	cli.PreParseBootstrapFlags(target, remaining)
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
