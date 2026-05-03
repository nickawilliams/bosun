package generate

import (
	"io"

	"github.com/spf13/cobra"
)

// WriteBashCompletion writes a Bash completion script for cmd to w.
func WriteBashCompletion(w io.Writer, cmd *cobra.Command) error {
	return cmd.GenBashCompletionV2(w, true)
}

// WriteZshCompletion writes a Zsh completion script for cmd to w.
func WriteZshCompletion(w io.Writer, cmd *cobra.Command) error {
	return cmd.GenZshCompletion(w)
}

// WriteFishCompletion writes a Fish completion script for cmd to w.
func WriteFishCompletion(w io.Writer, cmd *cobra.Command) error {
	return cmd.GenFishCompletion(w, true)
}
