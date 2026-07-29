package main

import (
	"context"
	"io"
	"os"

	"charm.land/fang/v2"
	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/spf13/cobra"
)

var version = "dev"

// helpLikeInvocation reports whether argv asks for output that must
// stay available with a broken config: help (flag or command), the
// version flag, or shell completion.
func helpLikeInvocation(target *cobra.Command, args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			// Everything after -- is positional.
			return false
		case "-h", "--help", "-v", "--version":
			return true
		}
	}
	if target == nil {
		return false
	}
	switch target.Name() {
	case "help", "completion", "__complete", "__completeNoDesc":
		return true
	}
	return false
}

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
	// Help / version / completion output must work even with a broken
	// config file — cobra's own help path short-circuits before
	// PersistentPreRunE ever loads config, and the eager Bootstrap
	// here would otherwise block `bosun --help` on a YAML parse error.
	if !helpLikeInvocation(target, os.Args[1:]) {
		if err := cli.Bootstrap(target); err != nil {
			cli.HandleError(err)
			os.Exit(1)
		}
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
