package ui

import "fmt"

// SetContext renders the root header immediately from the given
// context and begins the timeline. Call once at command init, before
// printing any cards. project is the resolved project name;
// workContext is the workspace display label (issue key or raw name);
// command is the human-readable command title.
func SetContext(project, workContext, command string) {
	if IsRaw() {
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
	fmt.Print(comfyPrefix() + root.Render())
	comfyBreak = true
}

// ResetContext is a no-op kept for test compatibility.
func ResetContext() {}
