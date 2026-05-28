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
			// --project must resolve before config.Load() because
			// Load() calls FindProjectRoot() to locate .bosun/config.yaml.
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

			if err := config.Load(); err != nil {
				return err
			}
			ui.ApplyColorMode(viper.GetString("display.color"))

			// Determine output mode: raw when stdout isn't a TTY, or
			// when the command explicitly declares raw output (annotation
			// or --output flag).
			raw := !ui.IsTerminal() ||
				cmd.Annotations["output"] == "raw" ||
				(cmd.Flag("output") != nil && cmd.Flag("output").Value.String() != "")

			if raw {
				ui.SetDefault(ui.NewRawReporter())
			} else {
				ui.SetCompactHeader(viper.GetBool("display.compact_header"))
				ui.BeginTimeline()
			}

			// Resolve context and render header. Runs for every
			// command so the breadcrumb is always present before
			// any RunE logic (including errors). Raw-mode commands
			// skip the header via SetContext's IsRaw() guard.
			cc, ccErr := resolveCommandContext(cmd)
			initHeader(cmd, cc)
			if ccErr != nil {
				return ccErr
			}
			return nil
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

	return cmd
}
