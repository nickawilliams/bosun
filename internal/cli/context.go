package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CommandContext holds the resolved context values for a command
// invocation. Populated once in PersistentPreRunE and stored on the
// cobra command's context. Commands retrieve it via commandContext(cmd).
// Empty strings indicate the stage was not resolved (command doesn't
// use it, or auto-detection found nothing).
type CommandContext struct {
	Project   string // Display name (e.g., "clearstory").
	Workspace string // Workspace name (e.g., "feature/EX-31432_put-provider").
	Issue     string // Issue key (e.g., "EX-31432").
}

type contextKey struct{}

// commandContext retrieves the CommandContext stored in PersistentPreRunE.
func commandContext(cmd *cobra.Command) CommandContext {
	if cc, ok := cmd.Context().Value(contextKey{}).(CommandContext); ok {
		return cc
	}
	return CommandContext{}
}

// resolveCommandContext performs staged resolution in dependency order:
// project → workspace → issue. Each stage only runs if the command
// registered the corresponding flag (via addProjectFlag, etc.).
//
// Called from PersistentPreRunE so the header renders before any
// command logic — including arg validation — can fail. The resolved
// context is stored on cmd.Context() for commands to retrieve via
// commandContext(cmd).
//
// Validation: when --workspace is explicitly set and a project root
// is known, verifies the workspace directory exists.
//
// Derivation: if workspace resolves but issue does not (from flag or
// env), the issue key is extracted from the workspace name before
// falling back to CWD or branch detection.
func resolveCommandContext(cmd *cobra.Command) (CommandContext, error) {
	var cc CommandContext

	// Stage 1: Project.
	if cmd.Flags().Lookup("project") != nil {
		cc.Project, _ = resolveProject(cmd)
	}

	// Stage 2: Workspace.
	if cmd.Flags().Lookup("workspace") != nil {
		cc.Workspace, _ = resolveWorkspaceName(cmd)

		// Validate: when the flag was explicitly set, check the
		// workspace directory exists under the project's workspace root.
		if f := cmd.Flags().Lookup("workspace"); f != nil && f.Changed && cc.Workspace != "" {
			if err := validateWorkspaceExists(cc.Workspace); err != nil {
				return cc, err
			}
		}
	}

	// Stage 3: Issue (non-interactive).
	if cmd.Flags().Lookup("issue") != nil {
		cc.Issue = resolveIssueSilent(cmd, cc.Workspace)
	}

	// Store on cmd context for retrieval by commandContext(cmd).
	cmd.SetContext(context.WithValue(cmd.Context(), contextKey{}, cc))

	return cc, nil
}

// RequireIssue ensures the issue is populated. If the pipeline did not
// resolve an issue, runs the interactive picker as a last resort.
// Returns an error if the issue is still empty after all attempts.
func (cc *CommandContext) RequireIssue() error {
	if cc.Issue != "" {
		return nil
	}
	if issue := pickOrPromptIssue(); issue != "" {
		cc.Issue = issue
		return nil
	}
	return fmt.Errorf(
		"issue not specified: use --issue, set BOSUN_ISSUE, or run from a workspace",
	)
}

// resolveIssueSilent resolves the issue key without interactive
// prompts. Chain: (1) --issue flag, (2) BOSUN_ISSUE env,
// (3) derive from resolved workspace name, (4) CWD workspace path,
// (5) git branch.
func resolveIssueSilent(cmd *cobra.Command, workspace string) string {
	// (1) Flag.
	if f := cmd.Flags().Lookup("issue"); f != nil && f.Changed {
		return f.Value.String()
	}

	// (2) Env / config via viper.
	if issue := viper.GetString("issue"); issue != "" {
		return issue
	}

	// (3) Derive from resolved workspace name.
	if workspace != "" {
		if issue := extractIssue(workspace); issue != "" {
			return issue
		}
	}

	// (4) CWD workspace path fallback.
	if issue := issueFromWorkspacePath(); issue != "" {
		return issue
	}

	// (5) Git branch fallback.
	if issue := issueFromBranch(); issue != "" {
		return issue
	}

	return ""
}

// validateWorkspaceExists checks that the workspace directory exists
// under the project's configured workspace root. Called when the
// --workspace flag was explicitly set (user input that could be wrong).
func validateWorkspaceExists(workspace string) error {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return nil // No project context — skip validation.
	}

	wsRoot := viper.GetString("workspace.root")
	if wsRoot == "" {
		return nil // Workspaces not configured — skip validation.
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	wsPath := filepath.Join(wsRoot, workspace)
	if _, err := os.Stat(wsPath); err != nil {
		return fmt.Errorf("workspace %q not found under %s", workspace, wsRoot)
	}
	return nil
}
