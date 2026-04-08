package cli

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
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

			ui.Muted("[stub] Would create %s issue: %q", issueType, title)
			return nil
		},
	}

	cmd.Flags().String("title", "", "issue title")
	cmd.Flags().String("description", "", "issue description")
	cmd.Flags().String("size", "", "issue size estimate")
	cmd.Flags().String("type", "story", "issue type (bug|story)")

	return cmd
}
