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
	ui.SetContext(cc.Project, workContext, commandTitle(cmd))
}

// commandTitle returns the human-readable display title for a
// command. Uses the headerAnnotationTitle annotation if set,
// otherwise the command name. Walks from the current command up
// to (but not including) the root, joining segments with " › ".
func commandTitle(cmd *cobra.Command) string {
	var segments []string
	for c := cmd; c != nil && c.Parent() != nil; c = c.Parent() {
		title := c.Annotations[headerAnnotationTitle]
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
