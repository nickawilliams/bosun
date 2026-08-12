package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// helpLikeInvocation decides whether output must survive a broken config.
// Getting it wrong is silent in the good case and only shows up when the
// config is already unreadable — i.e. exactly when the user needs `--help`
// to work. It has regressed once before (help and version output surviving
// a corrupt config), so the rules are pinned here rather than inferred.
func TestHelpLikeInvocation(t *testing.T) {
	cmd := func(name string) *cobra.Command { return &cobra.Command{Use: name} }

	cases := []struct {
		name   string
		target *cobra.Command
		args   []string
		want   bool
	}{
		{"help flag, short", cmd("status"), []string{"-h"}, true},
		{"help flag, long", cmd("status"), []string{"--help"}, true},
		{"version flag, short", cmd("status"), []string{"-v"}, true},
		{"version flag, long", cmd("status"), []string{"--version"}, true},
		{"flag among other args", cmd("status"), []string{"--issue", "X-1", "--help"}, true},

		{"help subcommand", cmd("help"), nil, true},
		{"completion subcommand", cmd("completion"), []string{"zsh"}, true},
		{"cobra completion hook", cmd("__complete"), []string{"sta"}, true},
		{"cobra completion hook, no desc", cmd("__completeNoDesc"), []string{"sta"}, true},

		{"ordinary command", cmd("status"), []string{"--issue", "X-1"}, false},
		{"no target and no flags", nil, []string{"whatever"}, false},
		{"no target but a help flag still counts", nil, []string{"--help"}, true},

		// Everything after `--` is positional, so a help-looking token there
		// is an argument to the command, not a request for help. This is the
		// rule most likely to be broken by a naive rewrite of the scan.
		{"help flag after -- is positional", cmd("status"), []string{"--", "--help"}, false},
		{"bare -- with no help flag", cmd("status"), []string{"--", "foo"}, false},
		{"-- does not rescue a help subcommand", cmd("help"), []string{"--"}, false},
		{"help flag before -- still counts", cmd("status"), []string{"--help", "--"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := helpLikeInvocation(tc.target, tc.args); got != tc.want {
				t.Errorf("helpLikeInvocation(%v, %v) = %v, want %v",
					tc.target, tc.args, got, tc.want)
			}
		})
	}
}
