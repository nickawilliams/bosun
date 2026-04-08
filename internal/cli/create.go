package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new issue",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			if title == "" {
				title = promptRequired("Title")
				if title == "" {
					return fmt.Errorf("title is required: use --title or run interactively")
				}
			}

			issueType, _ := cmd.Flags().GetString("type")

			fmt.Printf("[stub] Would create %s issue: %q\n", issueType, title)
			return nil
		},
	}

	cmd.Flags().String("title", "", "issue title")
	cmd.Flags().String("description", "", "issue description")
	cmd.Flags().String("size", "", "issue size estimate")
	cmd.Flags().String("type", "story", "issue type (bug|story)")

	return cmd
}
