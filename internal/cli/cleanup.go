package cli

import (
	"context"
	"fmt"

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

			// --- Plan + Apply ---
			plan := ui.NewPlan()
			for _, r := range repos {
				plan.Add(ui.PlanDestroy, "Remove Worktree", r.Name, branchName)
			}
			for _, r := range repos {
				plan.Add(ui.PlanDestroy, "Delete Branch", r.Name, fmt.Sprintf("%s (local + remote)", branchName))
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			actions := []PlanAction{
				func() error {
					mgr, err := newWorkspaceManager()
					if err != nil {
						return err
					}
					return mgr.Remove(context.Background(), branchName, wsRepos, force)
				},
			}

			if err := runPlanCard(cmd, plan, actions); err != nil {
				return nil
			}
			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes")
	return cmd
}
