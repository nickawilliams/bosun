package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// shellRunE wraps a cobra RunE in the single-program session shell:
// the body runs on the shell's worker goroutine and every UI
// primitive routes through one BubbleTea program, so phase
// transitions are frame swaps rather than program boundaries. Raw,
// plain, and capture modes bypass the shell inside ui.RunSession —
// the wrapped body runs directly and primitives take their existing
// non-session paths. See internal/ui/session.go for the contract.
func shellRunE(fn func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return ui.RunSession(func() error { return fn(cmd, args) })
	}
}
