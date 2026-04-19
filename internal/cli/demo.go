package cli

import (
	"errors"
	"time"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newDemoCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "demo",
		Short:  "Render all UI components for design iteration",
		Hidden: true,
		Annotations: map[string]string{
			headerAnnotationTitle: "UI components",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Root — title is read from cmd.Annotations["title"].
			rootCard(cmd).Print()

			// State glyphs
			ui.NewCard(ui.CardPending, "Pending task").Print()
			ui.NewCard(ui.CardSuccess, "Successful operation").Print()
			ui.NewCard(ui.CardSkipped, "Skipped step").Print()
			ui.NewCard(ui.CardFailed, "Failed operation").Print()
			ui.NewCard(ui.CardInput, "Answered prompt").Print()

			// Title + subtitle
			ui.NewCard(ui.CardSuccess, "Created branch").
				Subtitle("feature/ABC-123").
				Print()

			// Text body
			ui.NewCard(ui.CardSuccess, "Configured workspace").
				Text("repositories/api", "repositories/web", "repositories/infra").
				Print()

			// KV body (replaces the Panel+KV combo from `status`)
			ui.NewCard(ui.CardInfo, "ABC-123").
				Subtitle("Jira issue").
				KV(
					"Title", "Add user authentication flow",
					"Status", "In Progress",
					"Type", "Story",
					"URL", "https://jira.example.com/browse/ABC-123",
				).
				Print()

			// Stdout body
			ui.NewCard(ui.CardSuccess, "Ran: git status").
				Stdout(
					"On branch main",
					"Your branch is up to date with 'origin/main'.",
					"nothing to commit, working tree clean",
				).
				Print()

			// Stderr body
			ui.NewCard(ui.CardFailed, "Ran: go build ./...").
				Stderr(
					"internal/cli/foo.go:42:5: undefined: bar",
					"internal/cli/foo.go:51:2: undefined: baz",
				).
				Print()

			// Muted body
			ui.NewCard(ui.CardSkipped, "No .bosun/config found").
				Muted("falling back to global defaults").
				Print()

			// Live spinner — success
			_ = ui.RunCard("Fetching issue details", func() error {
				time.Sleep(1500 * time.Millisecond)
				return nil
			})

			// huh prompts — the title is printed as a CardInput card
			// so the "?" glyph only appears on the first row; huh
			// renders just the input widget with the accent │
			// connector. After the form exits we rewind over the
			// CardInput card and print the final CardSuccess card.
			var name string
			var confirmed bool

			nameTitle := "What should we call the new workspace?"
			rewind := ui.NewCard(ui.CardInput, nameTitle).Tight().PrintRewindable()
			if err := runForm(
				huh.NewInput().
					Description("Used as the worktree directory name").
					Placeholder("my-workspace").
					Value(&name),
			); err != nil {
				return err
			}
			rewind()
			ui.NewCard(ui.CardSuccess, nameTitle).
				Text(defaultStr(name, "(empty)")).
				Print()

			confirmTitle := "Create workspace now?"
			rewind = ui.NewCard(ui.CardInput, confirmTitle).Tight().PrintRewindable()
			if err := runForm(
				newConfirm().
					Affirmative("Yes").
					Negative("No").
					Value(&confirmed),
			); err != nil {
				return err
			}
			rewind()
			ui.NewCard(ui.CardSuccess, confirmTitle).
				Text(boolStr(confirmed)).
				Print()

			// Multi-field form — one root card labels the whole
			// operation; huh renders multiple fields inside a
			// single bordered block, each with its own sub-title.
			// On submit we rewind past the root card and print a
			// single success card whose KV body summarizes every
			// answered field.
			var (
				issueSummary  string
				issueType     string
				issuePriority string
			)
			createTitle := "Create a new issue"
			rewind = ui.NewCard(ui.CardInput, createTitle).
				Subtitle("Multi-field form").
				PrintRewindable()
			if err := runForm(
				huh.NewInput().
					Title("Summary").
					Placeholder("Add user authentication flow").
					Value(&issueSummary),
				huh.NewSelect[string]().
					Title("Type").
					Options(
						huh.NewOption("Story", "Story"),
						huh.NewOption("Bug", "Bug"),
						huh.NewOption("Task", "Task"),
					).
					Value(&issueType),
				huh.NewSelect[string]().
					Title("Priority").
					Options(
						huh.NewOption("Low", "Low"),
						huh.NewOption("Medium", "Medium"),
						huh.NewOption("High", "High"),
					).
					Value(&issuePriority),
			); err != nil {
				return err
			}
			rewind()
			ui.NewCard(ui.CardSuccess, createTitle).
				KV(
					"Summary", defaultStr(issueSummary, "(empty)"),
					"Type", defaultStr(issueType, "(empty)"),
					"Priority", defaultStr(issuePriority, "(empty)"),
				).
				Print()

			// Live spinner — failure
			_ = ui.RunCard("Pushing to remote", func() error {
				time.Sleep(1200 * time.Millisecond)
				return errors.New("permission denied")
			})

			return nil
		},
	}
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func boolStr(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
