package cli

import (
	"strings"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// headerAnnotationTitle is the cobra.Command.Annotations key used to
// declare the human-readable display title for a command. It is shown
// as the title of the opening header card on every command run and is
// distinct from cmd.Short (which is help text).
const headerAnnotationTitle = "title"

// rootCard returns a CardRoot card to open a command's output. The
// title is built as a breadcrumb from the command hierarchy (e.g.,
// "Bosun › Config › Show"). Any context strings are joined and shown
// as the subtitle — this is the right place for runtime values like
// an issue key or workspace name.
func rootCard(cmd *cobra.Command, context ...string) *ui.Card {
	card := ui.NewCard(ui.CardRoot, commandBreadcrumb(cmd))
	if len(context) > 0 {
		card.Subtitle(strings.Join(context, " · "))
	}
	return card
}

// commandBreadcrumb builds a display title from the command hierarchy.
// Each segment uses the annotation title if set, otherwise the command
// name. The root command always renders as "Bosun".
func commandBreadcrumb(cmd *cobra.Command) string {
	var segments []string
	for c := cmd; c != nil; c = c.Parent() {
		title := c.Annotations[headerAnnotationTitle]
		if title == "" {
			title = c.Name()
		}
		segments = append(segments, title)
	}

	// Reverse so root is first.
	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}

	// Root is always "bosun".
	if len(segments) > 0 {
		segments[0] = "bosun"
	}

	return strings.Join(segments, " › ")
}
