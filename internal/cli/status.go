package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show issue lifecycle status",
		Annotations: map[string]string{
			headerAnnotationTitle: "status",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}
			rootCard(cmd).Print()

			ctx := cmd.Context()

			// --- Issue details ---
			tracker, trackerErr := newIssueTracker()
			var detail issuepkg.Issue
			if trackerErr != nil {
				ui.Skip(fmt.Sprintf("Issue tracker: %v", trackerErr))
			} else {
				issueSlot := ui.NewSlot()
				if err := issueSlot.Run("Fetching issue", func() error {
					var e error
					detail, e = tracker.GetIssue(ctx, issue)
					return e
				}); err == nil {
					issueSlot.Clear()
					ui.NewCard(ui.CardInfo, fmt.Sprintf("%s: %s", detail.Type, detail.Key)).
						Subtitle(detail.Title).
						Text("").
						KV("Status", detail.Status, "URL", detail.URL).
						Print()
				}
			}

			// --- Repository branch status ---
			repositoryStatuses := resolveRepositoryStatuses(ctx)

			// --- PR status ---
			if len(repositoryStatuses) > 0 {
				host, hostErr := newCodeHost()
				if hostErr != nil {
					ui.Skip(fmt.Sprintf("Code host: %v", hostErr))
				} else {
					var prStatuses []prStatus
					prSlot := ui.NewSlot()
					_ = prSlot.Run("Fetching pull requests", func() error {
						prStatuses = collectPRStatus(ctx, host, repositoryStatuses)
						return nil
					})
					prSlot.Clear()
					if len(prStatuses) > 0 {
						fields := make(ui.Fields, 0, len(prStatuses))
						for _, ps := range prStatuses {
							value := fmt.Sprintf("#%d %s", ps.pr.Number, ps.pr.State)
							if ps.pr.Review != "" {
								value += " · " + ps.pr.Review
							}
							value += "\n" + ps.pr.URL
							fields = append(fields, ui.Field{
								Key:   ps.repositoryName,
								Value: value,
							})
						}
						ui.Details("Pull Requests", fields)
					}
				}
			}

			// TODO: CI/CD status (phase 6)

			return nil
		},
	}

	addIssueFlag(cmd)

	return cmd
}

type prStatus struct {
	repositoryName string
	pr             code.PullRequest
}

// resolveRepositoryStatuses determines repository branch status from the
// workspace (if CWD is at or below one) or from the current git repository
// (single-repository mode). Renders the Repositories detail card and returns
// the statuses for downstream use.
func resolveRepositoryStatuses(ctx context.Context) []workspace.RepositoryStatus {
	// Try workspace mode first.
	if mgr, err := newWorkspaceManager(); err == nil {
		cwd, _ := os.Getwd()
		if wsName, err := mgr.DetectWorkspace(cwd); err == nil {
			statuses, err := mgr.Status(ctx, wsName)
			if err != nil {
				ui.Skip(fmt.Sprintf("Workspace status: %v", err))
				return nil
			}
			if len(statuses) == 0 {
				ui.Skip("No repositories found in workspace " + wsName)
				return nil
			}
			renderRepositoryStatuses(statuses)
			return statuses
		}
		// Workspace configured but CWD is not inside one.
		ui.Skip("Not inside a workspace")
		return nil
	}

	// No workspace configured — single repository mode.
	cwd, _ := os.Getwd()
	g := git.New()
	branch, err := g.GetCurrentBranch(ctx, cwd)
	if err != nil {
		return nil
	}
	dirty, _ := g.IsDirty(ctx, cwd)
	statuses := []workspace.RepositoryStatus{{
		Name:   filepath.Base(cwd),
		Branch: branch,
		Dirty:  dirty,
		Path:   cwd,
	}}
	renderRepositoryStatuses(statuses)
	return statuses
}

func renderRepositoryStatuses(statuses []workspace.RepositoryStatus) {
	fields := make(ui.Fields, 0, len(statuses))
	for _, s := range statuses {
		status := "clean"
		if s.Dirty {
			status = "dirty"
		}
		fields = append(fields, ui.Field{
			Key:   s.Name,
			Value: s.Branch + " · " + status,
		})
	}
	ui.Details("Repositories", fields)
}

// collectPRStatus checks each repository for PRs matching its branch.
func collectPRStatus(ctx context.Context, host code.Host, statuses []workspace.RepositoryStatus) []prStatus {
	var results []prStatus

	for _, s := range statuses {
		identity, err := gh.ParseRemote(ctx, s.Path)
		if err != nil {
			continue
		}

		pr, err := host.GetPRForBranch(ctx, identity.Owner, identity.Name, s.Branch)
		if err != nil || pr.Number == 0 {
			continue
		}

		results = append(results, prStatus{repositoryName: s.Name, pr: pr})
	}

	return results
}
