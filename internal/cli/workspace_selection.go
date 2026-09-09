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
//  2. No pattern, explicit workspace context (the deprecated
//     --workspace flag or BOSUN_WORKSPACE) → set of one, no prompt.
//  3. No pattern, explicit issue context (--issue / BOSUN_ISSUE) →
//     the workspace carrying that issue key, no prompt. Explicit
//     issue context beats the implicit CWD detection of step 4 —
//     otherwise `cleanup --issue EX-2` run from inside workspace A
//     would destroy A while tearing down EX-2's preview env, a
//     cross-wired destroy.
//  4. No pattern, implicit workspace context (CWD) → set of one.
//  5. No pattern, no context → class default: destructive commands
//     prompt the batch picker interactively and error
//     non-interactively; read-only commands select everything and
//     never prompt.
//  6. A --status filter composes with the pattern: the pattern
//     selects the namespace (an exact name is a namespace of one,
//     routed through the batch flow so the filter still applies),
//     the filter selects by lifecycle. A filter with no pattern
//     implies batch scope over everything and counts as explicit
//     selection non-interactively. A filter combined with an
//     explicit single-target flag (--workspace, --issue) is refused:
//     silently broadening an explicitly named target to a filtered
//     population — or silently dropping the filter — would both
//     betray one of the two explicit asks.

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
	// pattern form. patternSource keeps conflict messages honest
	// about which spelling put the pattern there.
	patternSource := "a pattern argument"
	if f := cmd.Flags().Lookup("all"); f != nil && f.Changed {
		if all, _ := cmd.Flags().GetBool("all"); all {
			if pattern != "" {
				return workspaceSelection{}, fmt.Errorf(
					"--all and a pattern argument are mutually exclusive (--all is a deprecated alias for '**')")
			}
			ui.Warning(ui.PreserveCase("--all is deprecated; use a pattern instead: bosun %s '**'"), cmd.Name())
			pattern = "**"
			patternSource = "--all (a deprecated alias for the '**' pattern)"
		}
	}
	if f := cmd.Flags().Lookup("workspace"); f != nil && f.Changed {
		if pattern != "" {
			return workspaceSelection{}, fmt.Errorf(
				"%s and --workspace are mutually exclusive: the pattern is the workspace selection", patternSource)
		}
		if query.active() {
			return workspaceSelection{}, fmt.Errorf(
				"--workspace and --status are mutually exclusive: a filter names a population; select it with a pattern")
		}
		ui.Warning(ui.PreserveCase("--workspace is deprecated for %s; pass the workspace name as an argument: bosun %s '%s'"),
			cmd.Name(), cmd.Name(), cmd.Flags().Lookup("workspace").Value.String())
	}

	// Step 1 — explicit pattern. An exact name is single mode unless
	// a filter rides along: the filter must still apply (step 6), so
	// the exact name routes through the batch flow as a namespace of
	// one rather than silently dropping the user's guard condition.
	if pattern != "" {
		if f := cmd.Flags().Lookup("issue"); f != nil && f.Changed {
			return workspaceSelection{}, fmt.Errorf(
				"%s and --issue are mutually exclusive: the pattern is the workspace selection", patternSource)
		}
		if !patternHasGlob(pattern) && !query.active() {
			return workspaceSelection{exact: pattern}, nil
		}
		if _, err := compileWorkspacePattern(pattern); err != nil {
			return workspaceSelection{}, err
		}
		return workspaceSelection{batch: true, pattern: pattern}, nil
	}

	// Step 6 — a filter with no pattern implies batch scope and is
	// itself the explicit selection. An explicit single-target flag
	// alongside it is refused rather than silently broadened
	// (--workspace was handled above, next to its deprecation arm).
	if query.active() {
		if explicitIssueContext(cmd) {
			return workspaceSelection{}, fmt.Errorf(
				"--issue and --status are mutually exclusive: a filter names a population; select it with a pattern")
		}
		return workspaceSelection{batch: true, pattern: "**"}, nil
	}

	cc := commandContext(cmd)

	// Step 2 — explicit workspace context (flag/env).
	if explicitWorkspaceContext(cmd) && cc.Workspace != "" {
		return workspaceSelection{}, nil
	}

	// Step 3 — explicit issue context maps to its workspace, beating
	// the implicit CWD detection below.
	if explicitIssueContext(cmd) && cc.Issue != "" {
		name, err := workspaceForIssue(cc.Issue)
		if err != nil {
			return workspaceSelection{}, err
		}
		return workspaceSelection{exact: name}, nil
	}

	// Step 4 — implicit workspace context (CWD).
	if cc.Workspace != "" {
		return workspaceSelection{}, nil
	}

	// Step 5 — class default.
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

// explicitWorkspaceContext reports whether the run names a workspace
// explicitly — the (deprecated) --workspace flag or the
// BOSUN_WORKSPACE env var — as opposed to the CWD detection fallback.
// The distinction orders steps 2–4: an explicit flag outranks the
// issue mapping, which outranks the implicit CWD.
func explicitWorkspaceContext(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup("workspace"); f != nil && f.Changed {
		return true
	}
	return os.Getenv("BOSUN_WORKSPACE") != ""
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
// must not error or prompt, so this answers only the shape question
// (conflicting-flag combinations error in RunE and never render a
// scoped body; their title is moot).
func selectionLooksBatch(cmd *cobra.Command, cc CommandContext) bool {
	filtered := false
	if cmd.Flags().Lookup("status") != nil {
		statuses, _ := cmd.Flags().GetStringSlice("status")
		filtered = len(statuses) > 0
	}
	if args := cmd.Flags().Args(); len(args) > 0 {
		return patternHasGlob(args[0]) || filtered
	}
	if all, _ := cmd.Flags().GetBool("all"); all {
		return true
	}
	if filtered {
		return true
	}
	if explicitIssueContext(cmd) {
		return false
	}
	return cc.Workspace == ""
}
