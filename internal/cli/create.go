package cli

import (
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
					if err := runForm(fields...); err != nil {
						return err
					}
				}
			}

			if title == "" {
				return fmt.Errorf("title is required: use --title or run interactively")
			}
			ui.Header("create")

			project := viper.GetString("jira.project")
			if project == "" {
				return fmt.Errorf("jira.project not configured in .bosun/config.yaml")
			}

			if isDryRun(cmd) {
				ui.DryRun("Would create %s issue", issueType)
				ui.NewKV().
					Add("Project", project).
					Add("Title", title).
					Add("Description", description).
					Add("Size", size).
					Print()
				return nil
			}

			tracker, err := newIssueTracker()
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			created, err := ui.WithSpinnerResult("Creating issue...", func() (issuepkg.Issue, error) {
				return tracker.CreateIssue(ctx, issuepkg.CreateRequest{
					Project:     project,
					Title:       title,
					Description: description,
					Type:        issueType,
					Size:        size,
				})
			})
			if err != nil {
				return err
			}

			ui.Complete(fmt.Sprintf("Created %s", created.Key))
			ui.NewKV().
				Add("Title", created.Title).
				Add("Status", created.Status).
				Add("URL", created.URL).
				Print()

			return nil
		},
	}

	cmd.Flags().String("title", "", "issue title")
	cmd.Flags().String("description", "", "issue description")
	cmd.Flags().String("size", "", "issue size estimate")
	cmd.Flags().String("type", "story", "issue type (bug|story)")

	return cmd
}
