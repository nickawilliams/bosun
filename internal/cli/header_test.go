package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func TestCommandTitle(t *testing.T) {
	root := &cobra.Command{Use: "bosun"}
	config := &cobra.Command{
		Use:         "config",
		Annotations: map[string]string{headerAnnotationTitle: "Config"},
	}
	show := &cobra.Command{
		Use:         "show",
		Annotations: map[string]string{headerAnnotationTitle: "show"},
	}
	root.AddCommand(config)
	config.AddCommand(show)

	t.Run("leaf command with annotations", func(t *testing.T) {
		got := commandTitle(show, CommandContext{})
		if got != "Config › show" {
			t.Errorf("commandTitle(show, CommandContext{}) = %q, want %q", got, "Config › show")
		}
	})

	t.Run("mid-level command", func(t *testing.T) {
		got := commandTitle(config, CommandContext{})
		if got != "Config" {
			t.Errorf("commandTitle(config) = %q, want %q", got, "Config")
		}
	})

	t.Run("root command returns empty", func(t *testing.T) {
		got := commandTitle(root, CommandContext{})
		if got != "" {
			t.Errorf("commandTitle(root) = %q, want empty", got)
		}
	})

	t.Run("falls back to command name without annotation", func(t *testing.T) {
		plain := &cobra.Command{Use: "doctor"}
		root.AddCommand(plain)
		got := commandTitle(plain, CommandContext{})
		if got != "doctor" {
			t.Errorf("commandTitle(plain) = %q, want %q", got, "doctor")
		}
	})
}

// captureStdout swaps os.Stdout for an in-memory pipe for the
// duration of fn, returning everything written to it. Used to
// inspect the breadcrumb that renderErrorHeader emits.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	return <-done
}

func TestRenderErrorHeader(t *testing.T) {
	root := &cobra.Command{Use: "bosun"}
	plain := &cobra.Command{
		Use:         "doctor",
		Annotations: map[string]string{headerAnnotationTitle: "Doctor"},
	}
	hidden := &cobra.Command{
		Use: "captain",
		Annotations: map[string]string{
			headerAnnotationTitle:       "Captain On Deck!",
			headerAnnotationHideOnError: "true",
		},
	}
	root.AddCommand(plain)
	root.AddCommand(hidden)

	// Force non-raw rendering and a fresh header for each subtest.
	ui.SetDefault(ui.NewCardReporter())
	ui.SetCompactHeader(true)

	t.Run("plain command renders title in breadcrumb", func(t *testing.T) {
		ui.ResetContext()
		SetCurrentCommand(plain)
		out := captureStdout(t, renderErrorHeader)
		if !strings.Contains(out, "Doctor") {
			t.Errorf("expected breadcrumb to contain %q; got %q", "Doctor", out)
		}
	})

	t.Run("hide-on-error suppresses the title", func(t *testing.T) {
		ui.ResetContext()
		SetCurrentCommand(hidden)
		out := captureStdout(t, renderErrorHeader)
		if strings.Contains(out, "Captain") {
			t.Errorf("expected breadcrumb to omit %q; got %q", "Captain", out)
		}
	})

	t.Run("no current command falls back to bare header", func(t *testing.T) {
		ui.ResetContext()
		SetCurrentCommand(nil)
		out := captureStdout(t, renderErrorHeader)
		if strings.Contains(out, "Doctor") || strings.Contains(out, "Captain") {
			t.Errorf("expected bare breadcrumb; got %q", out)
		}
	})
}
