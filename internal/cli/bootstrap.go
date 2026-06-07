package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// currentCmd holds the cobra command targeted by the current
// invocation. Set by Bootstrap (called from main() before any cobra
// dispatch) and refreshed in PersistentPreRunE. Consumed by
// HandleError so error-path breadcrumbs reflect the user's intent
// even when the failure happened before PersistentPreRunE could run
// (cobra's ValidateArgs and fang's flag parsing both fire first).
var currentCmd *cobra.Command

// SetCurrentCommand records the cobra command for the current
// invocation. PersistentPreRunE refreshes it once cobra hands over
// the authoritative cmd; Bootstrap seeds it at main()-time via
// cmd.Find for error paths that bypass PersistentPreRunE entirely.
func SetCurrentCommand(cmd *cobra.Command) { currentCmd = cmd }

// bootstrapped guards Bootstrap so it runs exactly once per process.
// main() calls Bootstrap before fang.Execute so errors from cobra's
// ValidateArgs or fang's flag parsing render with the right UI mode;
// PersistentPreRunE also calls it as a safety net for entry points
// that bypass main() (notably the in-process test harness that calls
// cmd.Execute directly).
var bootstrapped bool

// ResetBootstrap allows tests to re-exercise Bootstrap between cases.
func ResetBootstrap() { bootstrapped = false }

// Bootstrap performs the process-wide setup that has to be in place
// before any cobra/fang dispatch — so a downstream Args validation
// or flag parse error doesn't reach HandleError with default UI
// state (full logo, compact_header off). Idempotent: the first
// caller (main() in production, PersistentPreRunE in tests) does
// the work; subsequent calls are no-ops. Pass the pre-resolved
// target command (or nil when no target could be inferred from the
// argv); PersistentPreRunE then runs the per-invocation work —
// resolving the command context and rendering the header — on top
// of the state Bootstrap established.
//
// Steps, in dependency order:
//
//  1. Register cmd as the current command so HandleError can reach
//     for it on any failure path.
//  2. Thread cmd's I/O streams to the UI layer (no-op when cmd is
//     nil; the UI keeps its default os.Stdout/Stderr/Stdin).
//  3. Honor --project (when set on cmd) before config.Load so the
//     project's .bosun/config.yaml lands in viper.
//  4. Load merged config (global + project).
//  5. Apply the user's color mode preference.
//  6. Decide raw vs. card rendering from TTY + per-command opt-in
//     and configure the default reporter / compact header / timeline.
func Bootstrap(cmd *cobra.Command) error {
	SetCurrentCommand(cmd)
	if bootstrapped {
		return nil
	}
	bootstrapped = true

	if cmd != nil {
		ui.SetStreams(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())

		if f := cmd.Flags().Lookup("project"); f != nil && f.Changed {
			abs, err := filepath.Abs(f.Value.String())
			if err != nil {
				return fmt.Errorf("--project: %w", err)
			}
			info, err := os.Stat(filepath.Join(abs, ".bosun"))
			if err != nil || !info.IsDir() {
				return fmt.Errorf("--project: no .bosun/ directory found in %s", abs)
			}
			config.ProjectRootOverride = abs
		}
	}

	if err := config.Load(); err != nil {
		return err
	}
	ui.ApplyColorMode(viper.GetString("display.color"))

	raw := !ui.IsTerminal()
	if cmd != nil {
		raw = raw ||
			cmd.Annotations["output"] == "raw" ||
			(cmd.Flag("output") != nil && cmd.Flag("output").Value.String() != "")
	}

	if raw {
		ui.SetDefault(ui.NewRawReporter())
	} else {
		ui.SetCompactHeader(viper.GetBool("display.compact_header"))
		ui.BeginTimeline()
	}
	return nil
}
