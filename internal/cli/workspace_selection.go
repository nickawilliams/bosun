package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// This file holds the unified selection pipeline for set-oriented
// commands (#120): the positional workspace pattern is the one
// selection grammar, the interactive multi-select picker is its
// interactive form, and only the unspecified-selection default varies
// by command class ("default scope set by consequence").
//
// The pipeline, in order:
//
//  1. Pattern argument → explicit selection. An exact name (no glob
//     metacharacters) is single mode; a glob is batch. The pattern
//     overrides workspace context, so `cleanup '**'` from inside a
//     workspace still reaches the batch flow.
//  2. No pattern, workspace context (CWD, BOSUN_WORKSPACE, or the
//     deprecated --workspace) → set of one, no prompt.
//  3. No pattern, explicit issue context (--issue / BOSUN_ISSUE) →
//     the workspace carrying that issue key, no prompt.
//  4. No pattern, no context → class default: destructive commands
//     prompt the batch picker interactively and error
//     non-interactively; read-only commands select everything and
//     never prompt.
//  5. A --status filter narrows the selection at any step; a filter
//     with no pattern implies batch scope and counts as explicit
//     selection non-interactively.

// selectionClass names the unspecified-selection default a command
// carries: what happens when nothing selects a workspace set.
type selectionClass int

const (
	// selectionDestructive: bare invocations prompt interactively and
	// error non-interactively — project scope is never implicit for a
	// command that destroys things.
	selectionDestructive selectionClass = iota
	// selectionReadOnly: bare invocations select everything and never
	// prompt — reading at the widest scope is free.
	selectionReadOnly
)

// workspaceSelection is the resolved answer to "which workspaces is
// this run about". Exactly one of the three shapes holds: batch with
// a pattern, single via an exact-name pattern (exact non-empty), or
// single via resolved workspace context (both zero).
type workspaceSelection struct {
	batch   bool
	pattern string // batch: the effective glob ("**" = everything)
	exact   string // single: explicit target from an exact-name pattern
}

// resolveWorkspaceSelection runs the selection pipeline for a
// command's invocation. It also owns the deprecation surface: --all
// is a deprecated alias for '**', and --workspace is redundant with
// an exact-name pattern on the commands that take one.
func resolveWorkspaceSelection(cmd *cobra.Command, args []string, query workspaceQuery, class selectionClass) (workspaceSelection, error) {
	pattern := ""
	if len(args) > 0 {
		pattern = args[0]
		if pattern == "" {
			return workspaceSelection{}, fmt.Errorf("empty workspace pattern (use '**' to select every workspace)")
		}
	}

	// Deprecated aliases. --all becomes '**'; --workspace keeps its
	// context-resolution behavior (step 2) but warns toward the
	// pattern form.
	if f := cmd.Flags().Lookup("all"); f != nil && f.Changed {
		if all, _ := cmd.Flags().GetBool("all"); all {
			if pattern != "" {
				return workspaceSelection{}, fmt.Errorf(
					"--all and a pattern argument are mutually exclusive (--all is a deprecated alias for '**')")
			}
			ui.Warning("--all is deprecated; use a pattern instead: bosun %s '**'", cmd.Name())
			pattern = "**"
		}
	}
	if f := cmd.Flags().Lookup("workspace"); f != nil && f.Changed {
		if pattern != "" {
			return workspaceSelection{}, fmt.Errorf(
				"a pattern argument and --workspace are mutually exclusive: the pattern is the workspace selection")
		}
		ui.Warning("--workspace is deprecated for %s; pass the workspace name as an argument: bosun %s '%s'",
			cmd.Name(), cmd.Name(), cmd.Flags().Lookup("workspace").Value.String())
	}

	// Step 1 — explicit pattern.
	if pattern != "" {
		if f := cmd.Flags().Lookup("issue"); f != nil && f.Changed {
			return workspaceSelection{}, fmt.Errorf(
				"a pattern argument and --issue are mutually exclusive: the pattern is the workspace selection")
		}
		if !patternHasGlob(pattern) {
			return workspaceSelection{exact: pattern}, nil
		}
		if _, err := compileWorkspacePattern(pattern); err != nil {
			return workspaceSelection{}, err
		}
		return workspaceSelection{batch: true, pattern: pattern}, nil
	}

	// Step 5 — a filter with no pattern implies batch scope and is
	// itself the explicit selection.
	if query.active() {
		return workspaceSelection{batch: true, pattern: "**"}, nil
	}

	// Step 2 — workspace context.
	cc := commandContext(cmd)
	if cc.Workspace != "" {
		return workspaceSelection{}, nil
	}

	// Step 3 — explicit issue context maps to its workspace.
	if explicitIssueContext(cmd) && cc.Issue != "" {
		name, err := workspaceForIssue(cc.Issue)
		if err != nil {
			return workspaceSelection{}, err
		}
		return workspaceSelection{exact: name}, nil
	}

	// Step 4 — class default.
	if class == selectionReadOnly {
		return workspaceSelection{batch: true, pattern: "**"}, nil
	}
	if isInteractive() {
		// Destructive bare invocation: batch scope whose picker is
		// the selection.
		return workspaceSelection{batch: true, pattern: "**"}, nil
	}
	return workspaceSelection{}, fmt.Errorf(
		"no workspace selection: pass a pattern (e.g. '**' for every workspace), --issue, or run from inside a workspace")
}

// explicitIssueContext reports whether the run names an issue
// explicitly — the --issue flag or the BOSUN_ISSUE env var. The
// silent derivation fallbacks (CWD path, git branch) don't count:
// they are conveniences for commands already inside a workspace, not
// a selection the user made.
func explicitIssueContext(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("issue"); f != nil && f.Changed {
		return true
	}
	return os.Getenv("BOSUN_ISSUE") != ""
}

// workspaceIssueKey extracts the issue key a workspace name carries:
// the trailing path segment first (e.g. "feature/EX-123_slug" →
// "EX-123"), the whole name as fallback. Shared with the observation
// path so the mapping can't drift.
func workspaceIssueKey(name string) string {
	if key := extractIssue(filepath.Base(name)); key != "" {
		return key
	}
	return extractIssue(name)
}

// workspaceForIssue resolves an explicit issue key to the one
// workspace whose name carries it. Zero or several matches are both
// errors — an explicit target that can't be resolved unambiguously
// must not fall through to a guess.
func workspaceForIssue(issueKey string) (string, error) {
	mgr, err := newWorkspaceManager()
	if err != nil {
		return "", err
	}
	names, err := mgr.List()
	if err != nil {
		return "", fmt.Errorf("listing workspaces: %w", err)
	}
	var matches []string
	for _, n := range names {
		if strings.EqualFold(workspaceIssueKey(n), issueKey) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no workspace found for issue %s", issueKey)
	default:
		return "", fmt.Errorf("issue %s maps to %d workspaces (%s); name one with a pattern argument",
			issueKey, len(matches), strings.Join(matches, ", "))
	}
}

// selectionLooksBatch mirrors resolveWorkspaceSelection's batch
// decision for header rendering — title resolvers run before RunE and
// must not error or prompt, so this answers only the shape question.
func selectionLooksBatch(cmd *cobra.Command, cc CommandContext) bool {
	if args := cmd.Flags().Args(); len(args) > 0 {
		return patternHasGlob(args[0])
	}
	if all, _ := cmd.Flags().GetBool("all"); all {
		return true
	}
	if cmd.Flags().Lookup("status") != nil {
		if statuses, _ := cmd.Flags().GetStringSlice("status"); len(statuses) > 0 {
			return true
		}
	}
	if f := cmd.Flags().Lookup("issue"); f != nil && f.Changed {
		return false
	}
	return cc.Workspace == ""
}
