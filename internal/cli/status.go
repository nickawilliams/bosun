package cli

import (
	"context"
	"encoding/json"
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
			rootCard(cmd).Print()
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}

			ctx := cmd.Context()

			// --- Issue details ---
			tracker, trackerErr := newIssueTracker()
			if trackerErr != nil {
				ui.Skip(fmt.Sprintf("issue tracker: %v", trackerErr))
			} else {
				_, err = fetchIssue(ctx, tracker, issue, func(d issuepkg.Issue, c *ui.Card) {
					c.Text("").KV("Status", d.Status, "URL", d.URL)
				})
				if err != nil {
					ui.Skip(fmt.Sprintf("issue details: %v", err))
				}
			}

			// --- Repository branch status ---
			repositoryStatuses := resolveRepositoryStatuses(ctx)

			// --- PR status ---
			if len(repositoryStatuses) > 0 {
				host, hostErr := newCodeHost()
				if hostErr != nil {
					ui.Skip(fmt.Sprintf("code host: %v", hostErr))
				} else {
					var prStatuses []prStatus
					prSlot := ui.NewSlot()
					_ = prSlot.Run("fetching pull requests", func() error {
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
						ui.Details("pull requests", fields)
					}
				}
			}

			// --- Preview environment ---
			if tracker != nil {
				if raw, err := tracker.GetProperty(ctx, issue); err == nil && raw != nil {
					var props struct {
						PreviewName string `json:"preview_name"`
					}
					if json.Unmarshal(raw, &props) == nil && props.PreviewName != "" {
						previewURL := renderStageURL("preview", props.PreviewName)
						value := props.PreviewName
						if previewURL != "" {
							value += "\n" + previewURL
						}
						ui.Details("preview", ui.Fields{{Key: "environment", Value: value}})
					}
				}
			}

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
				ui.Skip(fmt.Sprintf("workspace status: %v", err))
				return nil
			}
			if len(statuses) == 0 {
				ui.Skip("no repositories found in workspace " + wsName)
				return nil
			}
			renderRepositoryStatuses(statuses)
			return statuses
		}
		// Workspace configured but CWD is not inside one.
		ui.Skip("not inside a workspace")
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
	ui.Details("repositories", fields)
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
