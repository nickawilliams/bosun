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

// headerAnnotationHideOnError, when set to "true", suppresses the
// command's title segment in the breadcrumb when the invocation
// terminates via the error path (Args validation failure, flag
// parse error, etc.). Reserved for hidden commands where revealing
// the title in an error message would spoil discovery (e.g. easter
// eggs). Successful runs still render the full breadcrumb.
const headerAnnotationHideOnError = "title_hide_on_error"

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
	workContext := workContextSegment(cc)
	ui.SetContext(cc.Project, workContext, commandTitle(cmd, cc))
}

// renderErrorHeader bootstraps the breadcrumb for HandleError when
// PersistentPreRunE never rendered one (cobra ValidateArgs failure,
// fang flag parse failure, etc.). Uses currentCmd — captured by
// SetCurrentCommand in main() — to recover the command title and
// resolved context. Commands tagged with headerAnnotationHideOnError
// fall back to the bare "Bosun" header so the title isn't leaked
// alongside their error message (used by the captain easter egg).
// Idempotent: no-op when a header has already been rendered.
func renderErrorHeader() {
	cmd := currentCmd
	if cmd == nil || cmd.Annotations[headerAnnotationHideOnError] == "true" {
		ui.EnsureHeader()
		return
	}
	cc, _ := resolveCommandContext(cmd)
	ui.EnsureContext(cc.Project, workContextSegment(cc), commandTitle(cmd, cc))
}

// workContextSegment chooses the breadcrumb's work-context segment.
// Prefers the resolved issue key, then an issue key extracted from the
// workspace name (so commands that don't register --issue — like the
// workspace subcommands — still render a compact "EX-1234" instead of
// the full directory name), and finally the raw workspace name when no
// issue identifier is present.
func workContextSegment(cc CommandContext) string {
	if cc.Issue != "" {
		return cc.Issue
	}
	if cc.Workspace == "" {
		return ""
	}
	if issue := extractIssue(cc.Workspace); issue != "" {
		return issue
	}
	return cc.Workspace
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
