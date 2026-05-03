package generate

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func newTestRoot() *cobra.Command {
	root := &cobra.Command{Use: "tool", Short: "test tool"}
	root.AddCommand(&cobra.Command{Use: "create", Short: "Create a thing"})
	root.AddCommand(&cobra.Command{Use: "destroy", Short: "Destroy a thing"})
	return root
}

// Cobra's completion generators emit dynamic scripts that call back into the
// binary at runtime to fetch completions; subcommand names don't appear
// statically. The tests verify the runtime-callback framework is wired up.

func TestWriteBashCompletion(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteBashCompletion(&buf, newTestRoot()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"# bash completion V2 for tool",
		"__tool_get_completion_results",
		"__start_tool",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bash completion missing %q", want)
		}
	}
}

func TestWriteZshCompletion(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteZshCompletion(&buf, newTestRoot()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"#compdef tool", "_tool()", "__tool_debug"} {
		if !strings.Contains(out, want) {
			t.Errorf("zsh completion missing %q", want)
		}
	}
}

func TestWriteFishCompletion(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFishCompletion(&buf, newTestRoot()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"complete -c tool", "__tool_prepare_completions"} {
		if !strings.Contains(out, want) {
			t.Errorf("fish completion missing %q", want)
		}
	}
}
