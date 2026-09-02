package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// This file holds the shared workspace-scope grammar: the filter flags
// a command registers to operate over a filtered set of the project's
// workspaces, the query they resolve into, and the scope validation
// every command carrying --all applies. The query evaluates against
// workspaceState (the observed per-workspace aggregate) and knows
// nothing about what its consumer does with the matches — status
// renders them, cleanup destroys them, and the next consumer slots in
// without new plumbing.

// workspaceQuery is a resolved set of workspace filters. The zero
// value matches everything.
type workspaceQuery struct {
	// statuses holds bosun lifecycle keys (e.g. "done", "review") —
	// the config-stable vocabulary, not raw tracker status names.
	statuses []string
}

// addWorkspaceFilterFlags registers the workspace filter set on a
// command, declaring that it can narrow a project-scope run to the
// workspaces matching the filters. Mirrors how addWorkspaceFlag
// declares workspace context: registering the flags is the
// declaration, resolveWorkspaceQuery the resolution.
func addWorkspaceFilterFlags(cmd *cobra.Command) {
	cmd.Flags().StringSlice("status", nil,
		"filter workspaces by lifecycle status key (e.g. done; repeatable)")
}

// resolveWorkspaceQuery reads the filter flags into a workspaceQuery,
// validating each --status value against the lifecycle vocabulary
// (lifecycleStatusKeys + "done"). Commands that never registered the
// filter flags resolve to the match-everything zero query.
func resolveWorkspaceQuery(cmd *cobra.Command) (workspaceQuery, error) {
	if cmd.Flags().Lookup("status") == nil {
		return workspaceQuery{}, nil
	}
	statuses, _ := cmd.Flags().GetStringSlice("status")
	valid := lifecycleFilterKeys()
	for _, s := range statuses {
		if !slices.Contains(valid, s) {
			return workspaceQuery{}, fmt.Errorf(
				"unknown status key %q (valid: %s)",
				s, strings.Join(valid, ", "))
		}
	}
	return workspaceQuery{statuses: statuses}, nil
}

// lifecycleFilterKeys returns the full lifecycle vocabulary the
// --status filter accepts: the ordered stage keys plus the terminal
// "done".
func lifecycleFilterKeys() []string {
	return append(slices.Clone(lifecycleStatusKeys), "done")
}

// active reports whether the query narrows anything.
func (q workspaceQuery) active() bool {
	return len(q.statuses) > 0
}

// match reports whether an observed workspace satisfies the query.
// When the workspace cannot be evaluated at all — no issue key in its
// name, no status from the tracker, or a status outside the configured
// lifecycle vocabulary — it does not match, and reason says why, so
// callers can report the exclusion rather than silently dropping it. A
// clean non-match (evaluated, just not selected) returns an empty
// reason.
func (q workspaceQuery) match(ws workspaceState) (bool, string) {
	if !q.active() {
		return true, ""
	}
	if ws.issueKey == "" {
		return false, "no issue key in workspace name"
	}
	if ws.issue.Status == "" {
		return false, "issue status unknown (tracker unavailable or issue not found)"
	}
	key := lifecycleKeyForStatus(ws.issue.Status)
	if key == "" {
		return false, fmt.Sprintf(
			"issue status %q is not mapped to a lifecycle status", ws.issue.Status)
	}
	return slices.Contains(q.statuses, key), ""
}

// filterWorkspaces applies the query to observed workspace states,
// returning the matches in order. Unevaluable workspaces are reported
// as skips with their reason — the shared enforcement of the
// no-silent-drop rule — while evaluated non-matches drop without
// ceremony; deselecting them is what the filter is for.
func filterWorkspaces(states []workspaceState, q workspaceQuery) []workspaceState {
	var matched []workspaceState
	for _, ws := range states {
		ok, reason := q.match(ws)
		if ok {
			matched = append(matched, ws)
			continue
		}
		if reason != "" {
			ui.Skip(fmt.Sprintf("%s: %s", ws.name, reason))
		}
	}
	return matched
}

// resolveWorkspaceScope validates the scope grammar for a command that
// registered --all, and reports whether this run is project-scoped.
//
// The grammar, shared by every carrier so the same flags mean the same
// things everywhere:
//
//   - --all is explicit project scope, mutually exclusive with the
//     single-workspace targeting flags (--workspace, --issue).
//   - The filter flags require project scope. implicitProject reports
//     whether the command already operates at project scope without
//     --all (status outside a workspace); destructive commands pass
//     false so project scope is always an explicit ask.
func resolveWorkspaceScope(cmd *cobra.Command, implicitProject bool, q workspaceQuery) (bool, error) {
	all := false
	if f := cmd.Flags().Lookup("all"); f != nil {
		all, _ = cmd.Flags().GetBool("all")
	}
	if all {
		for _, name := range []string{"workspace", "issue"} {
			if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
				return false, fmt.Errorf(
					"--all and --%s are mutually exclusive: --all operates across every workspace in the project",
					name)
			}
		}
	}
	if q.active() && !all && !implicitProject {
		return false, fmt.Errorf(
			"workspace filters apply at project scope: pass --all to filter across the project's workspaces")
	}
	return all || implicitProject, nil
}

// observeWorkspaces fans fetch out across the named workspaces
// concurrently, preserving name order in the result. It is the
// observation seam project-scope commands share; the caller owns
// presentation (spinner, cards) and chooses how much fetch gathers —
// status observes the full workspaceState, cleanup's bulk filter only
// the issue detail.
func observeWorkspaces(ctx context.Context, names []string, fetch func(context.Context, string) workspaceState) []workspaceState {
	results := make([]workspaceState, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = fetch(ctx, name)
		}()
	}
	wg.Wait()
	return results
}
