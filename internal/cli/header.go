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
// title is read from cmd.Annotations["title"]; cmd.Short is used as
// a fallback so commands without an explicit title still render. Any
// context strings are joined and shown as the subtitle — this is the
// right place for runtime values like an issue key or workspace name.
func rootCard(cmd *cobra.Command, context ...string) *ui.Card {
	title := cmd.Annotations[headerAnnotationTitle]
	if title == "" {
		title = cmd.Short
	}
	card := ui.NewCard(ui.CardRoot, title)
	if len(context) > 0 {
		card.Subtitle(strings.Join(context, " · "))
	}
	return card
}
