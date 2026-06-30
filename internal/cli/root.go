package cli

import (
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

const groupLifecycle = "lifecycle"

// NewRootCmd creates the root bosun command. The version is propagated to
// the UI layer so cards can render it in their breadcrumb.
func NewRootCmd(version string) *cobra.Command {
	ui.AppVersion = version
	cobra.EnableCommandSorting = false

	cmd := &cobra.Command{
		Use:           "bosun",
		Short:         "Automate SDLC lifecycle tasks",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap is idempotent: in production main() already
			// ran it, so this call no-ops; in tests that bypass main
			// and call cmd.Execute directly, this is the entry point
			// that loads config and configures the UI. Either way,
			// SetCurrentCommand inside Bootstrap refreshes to the
			// authoritative cmd cobra is dispatching now.
			if err := Bootstrap(cmd); err != nil {
				return err
			}
			cc, ccErr := resolveCommandContext(cmd)
			initHeader(cmd, cc)
			return ccErr
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if !ui.IsRaw() {
				ui.EndTimeline()
			}
		},
	}

	cmd.PersistentFlags().Bool("dry-run", false, "show what would happen without making changes")
	cmd.PersistentFlags().BoolP("yes", "y", false, "skip confirmation prompt")
	cmd.PersistentFlags().Bool("interactive", false, "prompt for configurable values")

	cmd.AddGroup(
		&cobra.Group{ID: groupLifecycle, Title: "Lifecycle"},
	)

	// Lifecycle commands — ordered by lifecycle stage.
	lifecycle := []*cobra.Command{
		newCreateCmd(),
		newStartCmd(),
		newReviewCmd(),
		newPreviewCmd(),
		newPrereleaseCmd(),
		newReleaseCmd(),
		newCleanupCmd(),
	}
	for _, sub := range lifecycle {
		sub.GroupID = groupLifecycle
	}
	cmd.AddCommand(lifecycle...)

	// Utility commands — ungrouped so they merge with fang's
	// help/completion in the default "Commands" section.
	cmd.AddCommand(
		newConfigCmd(),
		newDoctorCmd(),
		newInitCmd(),
		newStatusCmd(),
		newWorkspaceCmd(),
	)

	// Hidden commands.
	cmd.AddCommand(newDemoCmd())
	cmd.AddCommand(newCaptainCmd())

	// extraCommands holds locally-registered, out-of-tree commands (e.g.
	// the untracked notify_demo.go sample-data command). The tracked tree
	// builds with this empty; local files append via init().
	cmd.AddCommand(extraCommands...)

	return cmd
}

// extraCommands is appended to the root command after the built-in set.
// It exists so untracked, local-only command files (kept out of version
// control) can register themselves via init() without modifying this
// tracked file. Empty in a clean checkout.
var extraCommands []*cobra.Command
