package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// headerRendered tracks whether SetContext has been called. Used by
// EnsureHeader to bootstrap a minimal header before error output
// when Cobra flag-parsing errors bypass PersistentPreRunE entirely.
var headerRendered bool

// SetContext renders the root header immediately from the given
// context and begins the timeline. Call once at command init, before
// printing any cards. project is the resolved project name;
// workContext is the workspace display label (issue key or raw name);
// command is the human-readable command title.
func SetContext(project, workContext, command string) {
	headerRendered = true
	if IsRaw() {
		if IsPlain() && command != "" {
			// plainReporter emits human-readable output to non-TTY
			// stdout. Route through the Reporter interface so it gets
			// a header line. rawReporter.Header is a no-op (correct
			// for machine-readable mode) — reached via IsRaw &&
			// !IsPlain, which falls through to the return below.
			// Guard on command != "": EnsureHeader() calls with an
			// empty command string (timeline marker only, no title),
			// and plainReporter.Header("") would emit a blank line.
			var ctx []string
			if project != "" {
				ctx = append(ctx, project)
			}
			if workContext != "" {
				ctx = append(ctx, workContext)
			}
			defaultReporter.Header(command, ctx...)
		}
		return
	}
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
func EnsureHeader() { EnsureContext("", "", "") }

// EnsureContext is the parameterized form of EnsureHeader: renders
// the header with the given breadcrumb segments if no header has
// been rendered yet. Idempotent: no-op when a header is already on
// screen or when output is fully silent (raw / capture mode). In
// plain mode, SetContext routes through the Reporter interface so a
// header line is emitted — and EnsureContext calls SetContext rather
// than short-circuiting, because IsRaw() is true for plain mode but
// the header still needs to be emitted.
func EnsureContext(project, workContext, command string) {
	if headerRendered {
		return
	}
	// IsRaw() is true for both rawReporter (silent — skip) and
	// plainReporter (emits output — proceed). SetContext handles the
	// distinction internally, so we always delegate when not yet
	// rendered.
	SetContext(project, workContext, command)
}

// ResetContext clears the headerRendered flag so tests starting with a
// fresh state don't inherit it from prior test cases.
func ResetContext() {
	headerRendered = false
}
