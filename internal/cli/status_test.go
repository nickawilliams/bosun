package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
)

func TestBuildWorkspaceStoryCard(t *testing.T) {
	t.Run("fetch failed falls back to key only", func(t *testing.T) {
		card := buildWorkspaceStoryCard(issuepkg.Issue{
			Key:  "PROJ-42",
			Type: "Story",
		}, "PROJ-42", false)

		out := ansi.Strip(card.Render())
		for _, want := range []string{"▲", "PROJ-42", "title unavailable"} {
			if !strings.Contains(out, want) {
				t.Errorf("card missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("fetch succeeded renders key and title", func(t *testing.T) {
		card := buildWorkspaceStoryCard(issuepkg.Issue{
			Key:   "PROJ-99",
			Title: "Add dark mode",
			Type:  "Story",
		}, "PROJ-99", true)

		out := ansi.Strip(card.Render())
		for _, want := range []string{"●", "PROJ-99", "Add dark mode"} {
			if !strings.Contains(out, want) {
				t.Errorf("card missing %q:\n%s", want, out)
			}
		}
	})
}
