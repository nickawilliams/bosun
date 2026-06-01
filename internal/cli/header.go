package cli

import (
	"strings"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// headerAnnotationTitle is the cobra.Command.Annotations key used to
// declare the human-readable display title for a command. It is shown
// in the breadcrumb header on every command run and is distinct from
// cmd.Short (which is help text).
const headerAnnotationTitle = "title"

// titleResolvers holds scope-aware title functions for commands whose
// breadcrumb title depends on the resolved CommandContext (e.g. status
// renders "Workspace Status" vs "Project Status"). The resolver runs
// at header-render time so the title reflects the actual scope, not
// the static annotation. Set via setTitleResolver; consumed by
// commandTitle.
var titleResolvers = map[*cobra.Command]func(CommandContext) string{}

// setTitleResolver registers a scope-aware title function for a
// command. The function receives the resolved CommandContext and
// returns the title segment to display. Returning "" falls back to
// the static annotation, then the command name.
func setTitleResolver(cmd *cobra.Command, fn func(CommandContext) string) {
	titleResolvers[cmd] = fn
}

// initHeader renders the root header immediately from the resolved
// context. Call once at the start of a command's RunE, after
// resolveCommandContext. Uses the already-resolved values from cc
// so nothing is re-resolved.
func initHeader(cmd *cobra.Command, cc CommandContext) {
	var workContext string
	if cc.Issue != "" {
		workContext = cc.Issue
	} else if cc.Workspace != "" {
		workContext = cc.Workspace
	}
	ui.SetContext(cc.Project, workContext, commandTitle(cmd, cc))
}

// commandTitle returns the human-readable display title for a
// command. Prefers a scope-aware resolver if registered, then the
// headerAnnotationTitle annotation, then the command name. Walks
// from the current command up to (but not including) the root,
// joining segments with " › ".
func commandTitle(cmd *cobra.Command, cc CommandContext) string {
	var segments []string
	for c := cmd; c != nil && c.Parent() != nil; c = c.Parent() {
		var title string
		if fn, ok := titleResolvers[c]; ok {
			title = fn(cc)
		}
		if title == "" {
			title = c.Annotations[headerAnnotationTitle]
		}
		if title == "" {
			title = c.Name()
		}
		segments = append(segments, title)
	}
	// Reverse so outermost command is first.
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}
	return strings.Join(segments, " › ")
}
