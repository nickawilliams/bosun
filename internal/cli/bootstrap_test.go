package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestPreParseBootstrapFlags locks the two properties the main()-time
// pre-parse depends on: it must surface --project/--output to
// Bootstrap, and it must not disturb the target's other flags —
// main() used to call target.ParseFlags directly, which made cobra's
// later authoritative parse a re-parse that appended a duplicate of
// every slice-typed flag value (--service api → [api api]).
func TestPreParseBootstrapFlags(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
		cmd.Flags().StringP("project", "p", "", "")
		cmd.Flags().StringSlice("service", nil, "")
		cmd.Flags().StringSlice("reviewer", nil, "")
		return cmd
	}

	t.Run("slice flags parse to single values", func(t *testing.T) {
		cmd := newCmd()
		args := []string{"--service", "api", "--reviewer", "alice", "--project", "/tmp/proj"}

		PreParseBootstrapFlags(cmd, args)

		// Bootstrap must already see the project value.
		if got, _ := cmd.Flags().GetString("project"); got != "/tmp/proj" {
			t.Errorf("project after pre-parse = %q, want %q", got, "/tmp/proj")
		}

		// The authoritative parse (what cobra runs during Execute).
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		if got, _ := cmd.Flags().GetStringSlice("service"); len(got) != 1 || got[0] != "api" {
			t.Errorf("service = %v, want [api]", got)
		}
		if got, _ := cmd.Flags().GetStringSlice("reviewer"); len(got) != 1 || got[0] != "alice" {
			t.Errorf("reviewer = %v, want [alice]", got)
		}
		if got, _ := cmd.Flags().GetString("project"); got != "/tmp/proj" {
			t.Errorf("project after authoritative parse = %q, want %q", got, "/tmp/proj")
		}
	})

	t.Run("shorthand and unknown flags", func(t *testing.T) {
		cmd := newCmd()
		args := []string{"-p", "/tmp/short", "--unknown-flag", "zzz", "--service", "api"}

		PreParseBootstrapFlags(cmd, args)

		if got, _ := cmd.Flags().GetString("project"); got != "/tmp/short" {
			t.Errorf("project via shorthand = %q, want %q", got, "/tmp/short")
		}
		if got, _ := cmd.Flags().GetStringSlice("service"); len(got) != 0 {
			t.Errorf("pre-parse must not touch slice flags; service = %v", got)
		}
	})

	t.Run("nil target and absent flags are no-ops", func(t *testing.T) {
		PreParseBootstrapFlags(nil, []string{"--project", "/tmp"})

		cmd := &cobra.Command{Use: "bare"}
		PreParseBootstrapFlags(cmd, []string{"--project", "/tmp"})
	})
}
