package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Begin work on an issue",
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}

			filterRepos, _ := cmd.Flags().GetStringSlice("repo")
			repos, err := resolveRepos(filterRepos)
			if err != nil {
				return err
			}

			// Build the branch name. For now, use the issue directly.
			// Pattern-based naming (branch.pattern config) will be added
			// when issue tracking provides the ticket type and title.
			branchName := issue
			fromHead, _ := cmd.Flags().GetBool("from-head")

			if isDryRun(cmd) {
				fmt.Printf("[dry-run] Would start work on %s\n", issue)
				fmt.Printf("  Branch: %s\n", branchName)
				fmt.Printf("  Repos: %s\n", repoNames(repos))
				fmt.Println("  [stub] Set issue status to In Progress")
				return nil
			}

			// Confirm when operating on multiple unfiltered repos.
			if len(repos) > 1 && len(filterRepos) == 0 {
				names := make([]string, len(repos))
				for i, r := range repos {
					names[i] = r.Name
				}
				fmt.Printf("Starting %s in %d repos:\n", issue, len(repos))
				fmt.Printf("  %s\n\n", strings.Join(names, ", "))
				fmt.Print("Proceed? [Y/n] ")
				scanner := bufio.NewScanner(os.Stdin)
				if scanner.Scan() {
					answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
					if answer != "" && answer != "y" && answer != "yes" {
						fmt.Println("Aborted.")
						return nil
					}
				}
			}

			mgr, err := newWorkspaceManager()
			if err != nil {
				return err
			}

			wsRepos := cliReposToWorkspaceRepos(repos)
			if err := mgr.Create(context.Background(), branchName, wsRepos, fromHead); err != nil {
				return err
			}

			fmt.Printf("Created workspace for %s\n", issue)
			for _, r := range repos {
				fmt.Printf("  %s → %s\n", r.Name, r.Path)
			}

			// TODO(nick): Set issue status to In Progress (phase 3)
			fmt.Println("[stub] Set issue status to In Progress")

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repo", nil, "filter repos to operate on")
	cmd.Flags().Bool("from-head", false, "branch from current HEAD instead of default branch")

	return cmd
}
