package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// headerRendered tracks whether SetContext has been called. Used by
// EnsureHeader to bootstrap a minimal header before error output
// when Cobra flag-parsing errors bypass PersistentPreRunE entirely.
var headerRendered bool

// HeaderRendered reports whether the root header has been rendered.
func HeaderRendered() bool { return headerRendered }

// SetContext renders the root header immediately from the given
// context and begins the timeline. Call once at command init, before
// printing any cards. project is the resolved project name;
// workContext is the workspace display label (issue key or raw name);
// command is the human-readable command title.
func SetContext(project, workContext, command string) {
	if IsRaw() {
		return
	}
	headerRendered = true
	title := "bosun"
	if command != "" {
		title += " › " + command
	}
	root := NewCard(CardRoot, title)
	root.breadcrumb = &breadcrumb{}
	if project != "" {
		root.breadcrumb.AddSegment(project)
	}
	if workContext != "" {
		root.breadcrumb.AddSegment(workContext)
	}
	conn := lipgloss.NewStyle().Foreground(Palette.Recessed).Render(cardConnector)
	fmt.Print(spacerPrefix() + root.Render() + " " + conn + "\n")
	needsSpacer = false
}

// EnsureHeader bootstraps a minimal timeline and header if one
// hasn't been rendered yet. Call from error paths that may fire
// before PersistentPreRunE completes (e.g., Cobra flag-parsing
// errors). No-ops if the header was already rendered or output
// is raw.
func EnsureHeader() {
	if headerRendered || IsRaw() {
		return
	}
	BeginTimeline()
	SetContext("", "", "")
}

// ResetContext is a no-op kept for test compatibility.
func ResetContext() {}
