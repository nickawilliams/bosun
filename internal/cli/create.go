package cli

import (
	"context"
	"fmt"

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
			size, _ := cmd.Flags().GetString("size")
			issueType, _ := cmd.Flags().GetString("type")

			if isInteractive() {
				var fields []huh.Field
				if title == "" {
					fields = append(fields, huh.NewInput().
						Title("Title").
						Value(&title))
				}
				if description == "" {
					fields = append(fields, huh.NewText().
						Title("Description").
						Value(&description))
				}
				if !cmd.Flags().Changed("type") {
					fields = append(fields, huh.NewSelect[string]().
						Title("Type").
						Options(
							huh.NewOption("Story", "story"),
							huh.NewOption("Bug", "bug"),
						).
						Value(&issueType))
				}
				if size == "" {
					fields = append(fields, huh.NewSelect[string]().
						Title("Size").
						Options(
							huh.NewOption("Small", "small"),
							huh.NewOption("Medium", "medium"),
							huh.NewOption("Large", "large"),
						).
						Value(&size))
				}

				if len(fields) > 0 {
					rewind := ui.NewCard(ui.CardInput, "Issue Details").PrintRewindable()
					if err := runForm(fields...); err != nil {
						return err
					}
					rewind()
				}
			}

			if title == "" {
				return fmt.Errorf("title is required: use --title or run interactively")
			}
			rootCard(cmd).Print()

			// --- Resolve ---
			project := viper.GetString("jira.project")
			if project == "" {
				return fmt.Errorf("jira.project not configured in .bosun/config.yaml")
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
					Label:  "Create Issue",
					Target: project,
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
							Size:        size,
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

	cmd.Flags().String("title", "", "issue title")
	cmd.Flags().String("description", "", "issue description")
	cmd.Flags().String("size", "", "issue size estimate")
	cmd.Flags().String("type", "story", "issue type (bug|story)")

	return cmd
}
