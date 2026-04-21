package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		Annotations: map[string]string{
			headerAnnotationTitle: "code review",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rootCard(cmd).Print()
			issue, err := resolveIssue(cmd)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			draft, _ := cmd.Flags().GetBool("draft")

			// --- Resolve ---

			var detail issuepkg.Issue
			tracker, trackerErr := newIssueTracker()
			if trackerErr == nil {
				detail, err = fetchIssue(ctx, tracker, issue)
				if err != nil {
					ui.Skip(fmt.Sprintf("Issue details: %v", err))
				}
			}

			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			repositories, err := resolveActiveRepositories(ctx, filterRepositories)
			if err != nil {
				return err
			}

			baseBranch := viper.GetString("pull_request.base")
			if baseBranch == "" {
				baseBranch = "main"
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("Code host: %v", hostErr))
			}

			// --- Pre-flight: resolve repos, branches, remotes ---

			g := git.New()

			type repoContext struct {
				repo     Repository
				branch   string
				owner    string
				repoName string
			}
			var resolved []repoContext

			for _, r := range repositories {
				branch, err := g.GetCurrentBranch(ctx, r.Path)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: cannot determine branch: %v", r.Name, err))
					continue
				}

				identity, err := gh.ParseRemote(ctx, r.Path)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: %v", r.Name, err))
					continue
				}

				resolved = append(resolved, repoContext{
					repo: r, branch: branch,
					owner: identity.Owner, repoName: identity.Name,
				})
			}

			// Build PR title and body from templates.
			var templateBranch string
			if len(resolved) > 0 {
				templateBranch = resolved[0].branch
			}
			prData := prTemplateData{
				IssueKey:   issue,
				IssueTitle: detail.Title,
				IssueType:  detail.Type,
				IssueURL:   detail.URL,
				Branch:     templateBranch,
				BaseBranch: baseBranch,
			}
			prTitle, _ := cmd.Flags().GetString("title")
			if prTitle == "" {
				prTitle = buildPRTitle(prData)
			}
			prBody, _ := cmd.Flags().GetString("body")
			if prBody == "" {
				prBody = buildPRBody(prData)
			}

			// Resolve reviewers and assignees from config + flags.
			reviewers := viper.GetStringSlice("pull_request.reviewers")
			if flagReviewers, _ := cmd.Flags().GetStringSlice("reviewer"); len(flagReviewers) > 0 {
				reviewers = append(reviewers, flagReviewers...)
			}
			teamReviewers := viper.GetStringSlice("pull_request.team_reviewers")
			if flagTeams, _ := cmd.Flags().GetStringSlice("team-reviewer"); len(flagTeams) > 0 {
				teamReviewers = append(teamReviewers, flagTeams...)
			}
			assignees := viper.GetStringSlice("pull_request.assignees")

			selfAssign := !viper.IsSet("pull_request.self_assign") || viper.GetBool("pull_request.self_assign")
			if cmd.Flags().Changed("self-assign") {
				selfAssign, _ = cmd.Flags().GetBool("self-assign")
			}
			if selfAssign && host != nil {
				username, err := host.GetAuthenticatedUser(ctx)
				if err != nil {
					ui.Fail(fmt.Sprintf("Self-assign: %v", err))
				} else if username != "" {
					duplicate := false
					for _, a := range assignees {
						if strings.EqualFold(a, username) {
							duplicate = true
							break
						}
					}
					if !duplicate {
						assignees = append(assignees, username)
					}
				}
			}

			// --- Pre-flight: push check ---

			type unpushedRepo struct {
				rc    repoContext
				count int // -1 = never pushed, >0 = commits ahead
			}
			var needsPush []unpushedRepo

			for _, rc := range resolved {
				n, err := g.UnpushedCommits(ctx, rc.repo.Path, rc.branch)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: %v", rc.repo.Name, err))
					continue
				}
				if n != 0 {
					needsPush = append(needsPush, unpushedRepo{rc: rc, count: n})
				}
			}

			if len(needsPush) > 0 {
				fields := make(ui.Fields, len(needsPush))
				for i, up := range needsPush {
					status := "not yet pushed"
					if up.count > 0 {
						status = fmt.Sprintf("%d unpushed commit(s)", up.count)
					}
					fields[i] = ui.Field{Key: up.rc.repo.Name, Value: status}
				}
				ui.Details("Unpushed Changes", fields)

				if !promptConfirm("Push before creating PRs?", true) {
					return fmt.Errorf("aborted: unpushed commits")
				}

				for _, up := range needsPush {
					if err := ui.RunCard(fmt.Sprintf("Pushing %s", up.rc.repo.Name), func() error {
						return g.Push(ctx, up.rc.repo.Path, up.rc.branch)
					}); err != nil {
						return fmt.Errorf("pushing %s: %w", up.rc.repo.Name, err)
					}
				}
			}

			// --- Plan + Apply ---

			createLabel := "Create Pull Request"
			if draft {
				createLabel = "Create Draft Pull Request"
			}

			var actions []Action

			type prResult struct {
				repo     string
				pr       code.PullRequest
				owner    string
				repoName string
			}
			var prResults []prResult

			if host != nil {
				for _, rc := range resolved {
					owner := rc.owner
					repoName := rc.repoName
					branch := rc.branch
					repoDisplayName := rc.repo.Name

					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						Label:  createLabel,
						Target: repoDisplayName,
						Assess: func(ctx context.Context) (ActionState, string, error) {
							existing, err := host.GetPRForBranch(ctx, owner, repoName, branch)
							if err != nil {
								return 0, "", err
							}
							if existing.Number > 0 {
								return ActionCompleted, fmt.Sprintf("#%d", existing.Number), nil
							}
							return ActionNeeded, fmt.Sprintf("%s → %s", branch, baseBranch), nil
						},
						Apply: func(ctx context.Context) error {
							pr, err := host.CreatePR(ctx, code.CreatePRRequest{
								Owner:      owner,
								Repository: repoName,
								Head:       branch,
								Base:       baseBranch,
								Title:      prTitle,
								Body:       prBody,
								Draft:      draft,
							})
							if err != nil {
								return err
							}
							prResults = append(prResults, prResult{
								repo: repoDisplayName, pr: pr,
								owner: owner, repoName: repoName,
							})
							return nil
						},
					})
				}
			}

			if !draft {
				if sa, ok := statusAction(tracker, issue, detail.Status, "review"); ok {
					actions = append(actions, sa)
				}
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			// Post-apply: request reviewers and add assignees.
			if host != nil && len(prResults) > 0 {
				for _, r := range prResults {
					if len(reviewers) > 0 || len(teamReviewers) > 0 {
						if err := host.RequestReviewers(ctx, r.owner, r.repoName, r.pr.Number, reviewers, teamReviewers); err != nil {
							ui.Fail(fmt.Sprintf("%s: reviewers: %v", r.repo, err))
						}
					}
					if len(assignees) > 0 {
						if err := host.AddAssignees(ctx, r.owner, r.repoName, r.pr.Number, assignees); err != nil {
							ui.Fail(fmt.Sprintf("%s: assignees: %v", r.repo, err))
						}
					}
				}
			}

			// Post-apply: notify review channel.
			if !draft && len(prResults) > 0 {
				items := make([]notify.Item, len(prResults))
				for i, r := range prResults {
					items[i] = notify.Item{
						Label:  r.repo,
						URL:    r.pr.URL,
						Detail: fmt.Sprintf("#%d", r.pr.Number),
					}
				}
				sendNotification(ctx, notify.Message{
					Channel:  viper.GetString("slack.channel_review"),
					IssueKey: issue,
					Title:    detail.Title,
					IssueURL: detail.URL,
					Items:    items,
				})
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	cmd.Flags().Bool("draft", false, "create draft pull request(s), skip status update and notifications")
	cmd.Flags().String("title", "", "override PR title")
	cmd.Flags().String("body", "", "override PR body")
	cmd.Flags().StringSlice("reviewer", nil, "request review from user (repeatable)")
	cmd.Flags().StringSlice("team-reviewer", nil, "request review from team (repeatable)")
	cmd.Flags().Bool("self-assign", false, "assign PR to yourself")
	return cmd
}
