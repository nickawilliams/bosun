package cli

import (
	"context"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new issue",
		Annotations: map[string]string{
			headerAnnotationTitle: "create issue",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			description, _ := cmd.Flags().GetString("description")
			issueType, _ := cmd.Flags().GetString("type")

			if isInteractive() {
				promptTitle := title == ""
				promptDescription := description == ""
				promptType := !cmd.Flags().Changed("type")

				var fields []huh.Field
				if promptTitle {
					fields = append(fields, huh.NewInput().
						Title("Title").
						Prompt(ui.FocusMarker).
						Value(&title))
				}
				if promptDescription {
					fields = append(fields, newText("Description").
						Value(&description))
				}
				if promptType {
					fields = append(fields, huh.NewSelect[string]().
						Title("Type").
						Options(
							huh.NewOption("Story", "story"),
							huh.NewOption("Bug", "bug"),
							huh.NewOption("Task", "task"),
						).
						Value(&issueType))
				}

				if len(fields) > 0 {
					rewind := ui.NewCard(ui.CardInput, "issue details").PrintRewindable()
					if err := runForm(fields...); err != nil {
						return err
					}
					rewind()
					// Swap the cleared form for a record of the submitted
					// input — the timeline retains what the user entered
					// (single prompts use ui.Selected; multi-field forms
					// use the consolidated KV card, like init's
					// integration cards). Only prompted fields appear —
					// flag-provided values never were in the form — and
					// empty answers are omitted (init's convention). The
					// description collapses to its first line; the full
					// text lands on the issue, not the timeline.
					pairs := make([]string, 0, 6)
					if promptTitle && title != "" {
						pairs = append(pairs, "Title", title)
					}
					if promptDescription && description != "" {
						pairs = append(pairs, "Description", firstLineSummary(description))
					}
					if promptType {
						pairs = append(pairs, "Type", ui.TitleCase(issueType))
					}
					if len(pairs) > 0 {
						ui.NewCard(ui.CardSuccess, "issue details").KV(pairs...).Print()
					}
				}
			}

			if title == "" {
				return fmt.Errorf("title is required: use --title or run interactively")
			}
			// --- Resolve ---
			project := viper.GetString("issue_tracker.project")
			if project == "" {
				return fmt.Errorf("issue_tracker.project not configured in .bosun/config.yaml")
			}

			tracker, err := newIssueTracker()
			if err != nil {
				return err
			}

			// --- Plan + Apply ---
			ctx := cmd.Context()
			var created issuepkg.Issue

			actions := []Action{
				{
					Op:     ui.PlanCreate,
					Action: "issue",
					Type:   "project",
					Name:   project,
					Assess: func(_ context.Context) (ActionState, string, error) {
						return ActionNeeded, fmt.Sprintf("%s: %q", issueType, title), nil
					},
					Apply: func(ctx context.Context) error {
						var createErr error
						created, createErr = tracker.CreateIssue(ctx, issuepkg.CreateRequest{
							Project:     project,
							Title:       title,
							Description: description,
							Type:        issueType,
						})
						return createErr
					},
				},
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			// Show created issue details.
			if created.Key != "" {
				ui.Details(created.Key, ui.NewFields(
					"Title", created.Title,
					"Status", created.Status,
					"URL", created.URL,
				))
			}

			return nil
		},
	}

	addProjectFlag(cmd)
	cmd.Flags().String("title", "", "issue title")
	cmd.Flags().String("description", "", "issue description")
	cmd.Flags().String("type", "story", "issue type (story|bug|task)")

	return cmd
}

// firstLineSummary reduces a multi-line value to its first non-empty
// line, appending an ellipsis when content was elided — the simplified
// representation record cards show for textarea input.
func firstLineSummary(s string) string {
	line, rest, cut := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if cut && strings.TrimSpace(rest) != "" {
		line += " …"
	}
	return line
}
