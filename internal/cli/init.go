package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new bosun project",
		Annotations: map[string]string{
			headerAnnotationTitle: "initialize project",
		},
		RunE: runInit,
	}

	addProjectFlag(cmd)
	cmd.Flags().Bool("no-detect", false, "skip auto-detection")
	cmd.Flags().Bool("quick", false, "only prompt for required values without defaults")
	cmd.Flags().String("workspace-root", "", "where workspaces are created")
	cmd.Flags().StringSlice("repositories", nil, "repository glob patterns (e.g. ./* or ~/Projects/myorg/*)")

	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	skipConfirm := isAutoApprove(cmd)
	quick, _ := cmd.Flags().GetBool("quick")
	noDetect, _ := cmd.Flags().GetBool("no-detect")

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	bosunDir := filepath.Join(cwd, ".bosun")

	// Detect reinit — if .bosun/ already exists, confirm before proceeding.
	reinit := false
	if _, err := os.Stat(bosunDir); err == nil {
		reinit = true
		if !skipConfirm {
			confirmed, err := NewDialog("Already Initialized").
				Description("Reconfigure project settings and integrations?").
				Default(false).
				Show()
			if err != nil {
				return err
			}
			if !confirmed {
				ui.Skip("keeping existing configuration")
				return nil
			}
		}
	}

	// Check if we're inside an existing bosun project.
	if existing := config.FindProjectRoot(); existing != "" && existing != cwd {
		return fmt.Errorf(
			"already inside a bosun project at %s (nested projects are not supported)",
			existing,
		)
	}

	// On reinit, load existing values for use as defaults.
	var existingRepos []string
	var existingWSRoot string
	if reinit {
		existingRepos = viper.GetStringSlice("repositories")
		existingWSRoot = viper.GetString("workspace.root")
	}

	// Resolve repository globs.
	repositoryGlobs, _ := cmd.Flags().GetStringSlice("repositories")
	var detectedGlobs []string
	if len(repositoryGlobs) == 0 && !noDetect {
		if repositories := detectRepositories(cwd); len(repositories) > 0 {
			ui.CompleteWithDetail("detected repositories", repositories)
			detectedGlobs = defaultRepositoryGlobs(cwd, repositories)
		}
	}

	// Resolve workspace root.
	wsRoot, _ := cmd.Flags().GetString("workspace-root")

	// Prompt for project settings. In quick mode on reinit, skip if
	// values are already in config.
	needRepositories := len(repositoryGlobs) == 0
	needWS := wsRoot == ""
	if quick && reinit {
		if needRepositories && len(existingRepos) > 0 {
			repositoryGlobs = existingRepos
			needRepositories = false
		}
		if needWS && existingWSRoot != "" {
			wsRoot = existingWSRoot
			needWS = false
		}
	}
	if (needRepositories || needWS) && isInteractive() {
		// Determine defaults: prefer existing config on reinit, then
		// detected globs, then a sensible fallback.
		repoDefault := strings.Join(detectedGlobs, ", ")
		if reinit && len(existingRepos) > 0 {
			repoDefault = strings.Join(existingRepos, ", ")
		} else if repoDefault == "" {
			repoDefault = "., ./*"
		}
		wsDefault := ".workspaces"
		if reinit && existingWSRoot != "" {
			wsDefault = existingWSRoot
		}

		// Wizard-style: project settings walk the user through each
		// required value one prompt at a time, with a Saved card per
		// field. Unlike integrations these settings are required for
		// bosun to function, so there's no Skip option — the goal is
		// to make sure the user sees and confirms each one rather than
		// race past them in a multi-field form.
		if needRepositories {
			input, repoField := newDefaultInput(repoDefault)
			rewind := ui.NewCard(ui.CardInput, "Repository Patterns").Tight().PrintRewindable()
			if err := runForm(input.
				Description("Comma-separated globs, e.g. ./* or ~/Projects/myorg/*")); err != nil {
				return err
			}
			rewind()
			val := repoField.Resolved()
			for _, g := range strings.Split(val, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repositoryGlobs = append(repositoryGlobs, trimmed)
				}
			}
			if val != "" {
				ui.Saved("Repository Patterns", val)
			}
		}
		if needWS {
			input, wsField := newDefaultInput(wsDefault)
			rewind := ui.NewCard(ui.CardInput, "Workspace Root").Tight().PrintRewindable()
			if err := runForm(input.
				Description("Directory where workspaces are created")); err != nil {
				return err
			}
			rewind()
			wsRoot = wsField.Resolved()
			if wsRoot != "" {
				ui.Saved("Workspace Root", wsRoot)
			}
		}
	}

	// Apply defaults for anything still unresolved.
	if len(repositoryGlobs) == 0 && len(detectedGlobs) > 0 {
		repositoryGlobs = detectedGlobs
	}
	if len(repositoryGlobs) == 0 && isInteractive() {
		input, err := promptDefault(
			"No repositories detected. Enter repository patterns (comma-separated, or leave blank)",
			"")
		if err != nil {
			return err
		}
		if input != "" {
			for _, g := range strings.Split(input, ",") {
				if trimmed := strings.TrimSpace(g); trimmed != "" {
					repositoryGlobs = append(repositoryGlobs, trimmed)
				}
			}
		}
	}
	if isDryRun(cmd) {
		ui.DryRun("would initialize bosun project")
		fields := ui.NewFields(
			"Config", ".bosun/config.yaml",
			"Repositories", strings.Join(repositoryGlobs, ", "),
		)
		if wsRoot != "" {
			fields = append(fields, ui.Field{Key: "Workspace Root", Value: wsRoot})
		}
		ui.Details("", fields)
		return nil
	}

	// Create .bosun/ directory.
	if err := os.MkdirAll(bosunDir, 0o755); err != nil {
		return fmt.Errorf("creating .bosun/: %w", err)
	}

	// Write config — fresh init creates the template; reinit does
	// targeted updates to preserve existing service configuration.
	configPath := filepath.Join(bosunDir, "config.yaml")
	if reinit {
		if len(repositoryGlobs) > 0 {
			if err := setConfigListValue(configPath, "repositories", repositoryGlobs); err != nil {
				return fmt.Errorf("updating repositories: %w", err)
			}
		}
		if wsRoot != "" {
			if err := setConfigValue(configPath, "workspace.root", wsRoot); err != nil {
				return fmt.Errorf("updating workspace.root: %w", err)
			}
		}
	} else {
		if err := writeInitConfig(configPath, wsRoot, repositoryGlobs); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}
	}

	// Per-field Saved cards above already told the user what they
	// set; the Wrote line confirms where the project-settings write
	// landed, matching the format used by config set / unset and the
	// per-integration confirmation below.
	printConfigWriteConfirmation("Wrote", "Project Settings", "to", configPath)
	if len(repositoryGlobs) == 0 {
		ui.Skip("no repositories configured — add patterns to " + configPath)
	}

	// Service configuration wizard — runs unless --yes.
	if isInteractive() {
		// Reload config so resolveGroup can read/write the new file.
		if err := config.Load(); err != nil {
			return err
		}

		for _, ig := range serviceInitGroups {
			group, ok := lookupGroup(ig.Group)
			if !ok {
				continue
			}

			if quick {
				// Quick mode: only resolve missing required keys.
				if err := resolveGroup(ig.Group, group); err != nil {
					return err
				}
				continue
			}

			if err := configureIntegration(ig, group); err != nil {
				return err
			}
		}
	}

	// Style the command portion of each hint distinctly (bold +
	// primary) so it pops against the muted surrounding wording —
	// the user's eye lands on what to type before reading why.
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(ui.Palette.Primary)
	hint := func(command, purpose string) string {
		return mutedStyle.Render("Run ") + cmdStyle.Render(command) + mutedStyle.Render(" "+purpose)
	}

	ui.NewCard(ui.CardInfo, "next steps").
		Text(hint("bosun doctor", "to verify connectivity")).
		Text(hint("bosun start --issue <issue>", "to begin work")).
		Print()

	return nil
}

// configureIntegration runs the gate form for one integration, then
// either skips it (SkipValue card) or proceeds with the silent resolve
// + consolidated card pattern. Integrations with a custom Setup
// callback (CI/CD) keep their non-silent flow so the callback's
// per-step cards still appear; the consolidated card is suppressed in
// that case so it doesn't leave the Setup's saves dangling beneath an
// out-of-order summary.
func configureIntegration(ig initGroup, group ConfigGroup) error {
	providerKey, providerOK := findGroupProviderKey(group)
	if !providerOK {
		// No provider key in this group — fall back to the plain
		// yes/no Dialog. Doesn't apply to any current schema group but
		// stays defensive against future shape changes.
		confirmed, err := promptConfirm(fmt.Sprintf("Configure %s?", ig.Label), false)
		if err != nil {
			return err
		}
		if !confirmed {
			ui.NewCard(ui.CardSkipped, ig.Label).Muted("(skipped)").Print()
			return nil
		}
		if err := resolveGroupReconfigure(ig.Group, group); err != nil {
			return err
		}
		if ig.Setup != nil {
			return ig.Setup()
		}
		return nil
	}

	provider, skipped, err := promptIntegrationGate(ig.Label, providerKey)
	if err != nil {
		return err
	}
	if skipped {
		// Title-on-first-line + "skipped" as a muted body line on the
		// second — same shape ui.Saved uses for confirmation cards.
		// Reads less compressed than the inline-dot form when sitting
		// between integration cards that themselves run several lines.
		ui.NewCard(ui.CardSkipped, ig.Label).Muted("(skipped)").Print()
		return nil
	}

	// Stage provider in viper so the form's defaults reflect it; the
	// actual disk write is deferred until after the form so the
	// whole integration lands in one read-modify-write pass.
	providerFK := fullKey(ig.Group, providerKey)
	viper.Set(providerFK, provider)

	if ig.Setup != nil {
		// CI/CD path: provider needs to be on disk before the setup
		// callback's per-step prompts run (they read the config to
		// decide their flow). Use the standard non-silent resolve
		// for the rest, then hand off to the callback.
		if err := saveConfigKeyMode(providerFK, providerKey.Label, provider, true); err != nil {
			return err
		}
		if err := resolveGroupReconfigure(ig.Group, group); err != nil {
			return err
		}
		return ig.Setup()
	}

	formLabel := "Configure " + ui.TitleCase(provider)
	kvs, err := resolveGroupAsForm(ig.Group, formLabel, group)
	if err != nil {
		return err
	}

	// Merge the gate's provider pick with whatever the form collected
	// and persist everything in one write. Groups whose only non-
	// provider keys are env-only secrets (e.g. code_host with just a
	// token) still land here because the provider is in kvs.
	kvs[providerFK] = provider

	configPath, err := configPathForScope(false)
	if err != nil {
		return err
	}
	if err := setConfigValues(configPath, kvs); err != nil {
		return err
	}

	// Consolidated KV card shows the user the values they just
	// entered (the form rewinds on submit, so they need this static
	// summary), then the Wrote line confirms where on disk the
	// section landed — same format the project-settings write uses
	// above and config set/unset use elsewhere.
	emitIntegrationCard(ig.Label, ig.Group, group)
	printConfigWriteConfirmation("Wrote", ui.TitleCase(group.Label), "to", configPath)
	return nil
}

// emitIntegrationCard renders the consolidated post-configuration
// card for an integration: CardSuccess with a KV body listing every
// configured key. Empty optional keys are omitted so the card only
// shows what was actually set.
//
// Secret keys aren't persisted (bosun has no keychain integration —
// they live in env vars that the user manages outside of bosun), so
// the card reports the env var's current state instead of a captured
// value: "•••••••• (from GITHUB_TOKEN)" when set, or "set GITHUB_TOKEN
// to authenticate" when not. This makes the durable contract — env
// vars, not the config file — visible at the moment the user is
// thinking about it.
func emitIntegrationCard(label, groupName string, group ConfigGroup) {
	var pairs []string
	for _, ck := range group.Keys {
		fk := fullKey(groupName, ck)
		if ck.Secret {
			if ck.EnvVar == "" {
				continue
			}
			if os.Getenv(ck.EnvVar) != "" {
				pairs = append(pairs, ck.Label, "•••••••• (from "+ck.EnvVar+")")
			} else {
				pairs = append(pairs, ck.Label, "set "+ck.EnvVar+" to authenticate")
			}
			continue
		}
		val := viper.GetString(fk)
		if val == "" {
			continue
		}
		pairs = append(pairs, ck.Label, val)
	}
	if len(pairs) == 0 {
		return
	}
	ui.NewCard(ui.CardSuccess, label).KV(pairs...).Print()
}

// findGroupProviderKey returns the schema's "provider" key for a group
// if one exists. The gate form's select widget renders over its
// Options, and the consolidated card uses its Label.
func findGroupProviderKey(group ConfigGroup) (ConfigKey, bool) {
	for _, ck := range group.Keys {
		if ck.Key == "provider" {
			return ck, true
		}
	}
	return ConfigKey{}, false
}

// initGroup describes an optional service group for the init wizard.
type initGroup struct {
	Label    string       // Human-readable name for the confirmation prompt.
	Group    string       // Schema group name (e.g., "issue_tracker").
	Setup    func() error // Custom setup flow, replaces resolveGroup when set.
}

// serviceInitGroups defines the ordered list of optional service groups
// presented during interactive init.
var serviceInitGroups = []initGroup{
	{Label: "issue tracker", Group: "issue_tracker"},
	{Label: "code host", Group: "code_host"},
	{Label: "notifications", Group: "notification"},
	{Label: "CI/CD", Group: "cicd", Setup: setupGitHubActions},
}

// detectRepositories scans a directory for git repositories: the directory
// itself (if it contains .git/) and immediate children that do.
func detectRepositories(dir string) []string {
	var repositories []string

	// Check if the directory itself is a repository.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		repositories = append(repositories, filepath.Base(dir)+" (root)")
	}

	// Check children.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return repositories
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
			repositories = append(repositories, entry.Name())
		}
	}
	return repositories
}

// defaultRepositoryGlobs returns the default repository glob patterns based
// on what was detected. Uses "." for the root repository and "./*" for children.
func defaultRepositoryGlobs(dir string, detected []string) []string {
	var globs []string
	hasRoot := false
	hasChildren := false

	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		hasRoot = true
	}
	for _, d := range detected {
		if !strings.HasSuffix(d, "(root)") {
			hasChildren = true
			break
		}
	}

	if hasRoot {
		globs = append(globs, ".")
	}
	if hasChildren {
		globs = append(globs, "./*")
	}
	return globs
}

// writeInitConfig writes the initial .bosun/config.yaml.
func writeInitConfig(path, wsRoot string, repositoryGlobs []string) error {
	var b strings.Builder

	b.WriteString("# Repository patterns (globs resolved to directories containing .git/)\n")
	b.WriteString("repositories:\n")
	if len(repositoryGlobs) > 0 {
		for _, g := range repositoryGlobs {
			fmt.Fprintf(&b, "  - %s\n", g)
		}
	} else {
		b.WriteString("  # - .          # this directory is a repository\n")
		b.WriteString("  # - ./*        # child directories that are repositories\n")
	}

	if wsRoot != "" {
		b.WriteString("\n# Where workspaces are created (relative to project root)\n")
		b.WriteString("workspace:\n")
		fmt.Fprintf(&b, "  root: %s\n", wsRoot)
	} else {
		b.WriteString("\n# Uncomment to enable worktree-based workspaces:\n")
		b.WriteString("# workspace:\n")
		b.WriteString("#   root: .workspaces\n")
	}

	b.WriteString("\n# Uncomment and configure as needed:\n")
	b.WriteString("# issue_tracker:\n")
	b.WriteString("#   provider: jira\n")
	b.WriteString("#   project: PROJ\n")
	b.WriteString("#\n")
	b.WriteString("# notification:\n")
	b.WriteString("#   provider: slack\n")
	b.WriteString("#   channel_review: code-review\n")
	b.WriteString("#   channel_release: releases\n")

	return os.WriteFile(path, []byte(b.String()), 0o644)
}
