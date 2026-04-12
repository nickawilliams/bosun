package cli

import (
	"context"

	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove workspace and feature branches",
		Annotations: map[string]string{
			headerAnnotationTitle: "cleanup",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd, issue).Print()

			repos, err := resolveRepos(nil)
			if err != nil {
				return err
			}

			branchName := issue
			force, _ := cmd.Flags().GetBool("force")

			if isDryRun(cmd) {
				ui.NewCard(ui.CardInfo, "Would remove workspace").
					Subtitle("dry-run").
					KV("Workspace", branchName, "Repos", repoNames(repos)).
					Print()
				return nil
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			return ui.RunCard("Removing workspace", func() error {
				return mgr.Remove(context.Background(), branchName, wsRepos, force)
			})
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	return cmd
}
