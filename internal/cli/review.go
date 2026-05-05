package cli

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
					ui.Skip(fmt.Sprintf("issue details: %v", err))
				}
			}

			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			repositories, err := resolveActiveRepositories(ctx, filterRepositories)
			if err != nil {
				return err
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("code host: %v", hostErr))
			}

			// --- Pre-flight: resolve repos, branches, remotes ---

			gitClient := git.New()
			r := ui.Default()

			type repoContext struct {
				repo     Repository
				branch   string
				owner    string
				repoName string
			}
			var resolved []repoContext

			r.Group("resolve repository identities", func(g ui.Reporter) {
				for _, repo := range repositories {
					branch, err := gitClient.GetCurrentBranch(ctx, repo.Path)
					if err != nil {
						g.Fail(fmt.Sprintf("%s: %v", repo.Name, err))
						continue
					}

					identity, err := gh.ParseRemote(ctx, repo.Path)
					if err != nil {
						g.Fail(fmt.Sprintf("%s: %v", repo.Name, err))
						continue
					}

					resolved = append(resolved, repoContext{
						repo: repo, branch: branch,
						owner: identity.Owner, repoName: identity.Name,
					})
					g.Selected(repo.Name, fmt.Sprintf("%s → %s/%s", branch, identity.Owner, identity.Name))
				}
			})

			// Use first repo's identity for API calls (list endpoints).
			var apiOwner, apiRepo string
			if len(resolved) > 0 {
				apiOwner = resolved[0].owner
				apiRepo = resolved[0].repoName
			}

			// --- Resolve PR metadata from flags, config, and interactive prompts ---

			baseBranch, _ := cmd.Flags().GetString("base")
			if baseBranch == "" {
				baseBranch = viper.GetString("pull_request.base")
			}
			if baseBranch == "" {
				baseBranch = "main"
			}
			if forceInteractive(cmd) && !cmd.Flags().Changed("base") && host != nil {
				selected, err := typeaheadSelect("Base Branch", baseBranch, func() ([]string, error) {
					return host.ListBranches(ctx, apiOwner, apiRepo)
				})
				if err != nil {
					return err
				}
				if selected != "" {
					baseBranch = selected
				}
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
			if forceInteractive(cmd) && !cmd.Flags().Changed("title") {
				val, err := typeaheadInput("Title", prTitle)
				if err != nil {
					return err
				}
				prTitle = val
			}

			prBody, _ := cmd.Flags().GetString("body")
			if prBody == "" {
				prBody = buildPRBody(prData)
			}
			if forceInteractive(cmd) && !cmd.Flags().Changed("body") {
				val, err := typeaheadText("Body", prBody)
				if err != nil {
					return err
				}
				prBody = val
			}

			// Resolve reviewers and assignees from config + flags.
			reviewers := viper.GetStringSlice("pull_request.reviewers")
			if flagReviewers, _ := cmd.Flags().GetStringSlice("reviewer"); len(flagReviewers) > 0 {
				reviewers = append(reviewers, flagReviewers...)
			}
			if forceInteractive(cmd) && !cmd.Flags().Changed("reviewer") && host != nil {
				selected, err := typeaheadMultiSelect("Reviewers", reviewers, func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				})
				if err != nil {
					return err
				}
				reviewers = selected
			}

			teamReviewers := viper.GetStringSlice("pull_request.team_reviewers")
			if flagTeams, _ := cmd.Flags().GetStringSlice("team-reviewer"); len(flagTeams) > 0 {
				teamReviewers = append(teamReviewers, flagTeams...)
			}
			if forceInteractive(cmd) && !cmd.Flags().Changed("team-reviewer") && host != nil {
				selected, err := typeaheadMultiSelect("Team Reviewers", teamReviewers, func() ([]string, error) {
					return host.ListTeams(ctx, apiOwner)
				})
				if err != nil {
					return err
				}
				teamReviewers = selected
			}

			assignees := viper.GetStringSlice("pull_request.assignees")
			if flagAssignees, _ := cmd.Flags().GetStringSlice("assignee"); len(flagAssignees) > 0 {
				assignees = append(assignees, flagAssignees...)
			}

			// Resolve self-assign before the interactive prompt so the
			// current user appears pre-selected in the list.
			selfAssign := !viper.IsSet("pull_request.self_assign") || viper.GetBool("pull_request.self_assign")
			if cmd.Flags().Changed("self-assign") {
				selfAssign, _ = cmd.Flags().GetBool("self-assign")
			}
			if selfAssign && host != nil {
				username, err := host.GetAuthenticatedUser(ctx)
				if err != nil {
					ui.Fail(fmt.Sprintf("self-assign: %v", err))
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

			if forceInteractive(cmd) && !cmd.Flags().Changed("assignee") && host != nil {
				selected, err := typeaheadMultiSelect("Assignees", assignees, func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				})
				if err != nil {
					return err
				}
				assignees = selected
			}

			// --- Pre-flight: push check ---

			type unpushedRepo struct {
				rc    repoContext
				count int // -1 = never pushed, >0 = commits ahead
			}
			var needsPush []unpushedRepo

			for _, rc := range resolved {
				n, err := gitClient.UnpushedCommits(ctx, rc.repo.Path, rc.branch)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: %v", rc.repo.Name, err))
					continue
				}
				if n != 0 {
					needsPush = append(needsPush, unpushedRepo{rc: rc, count: n})
				}
			}

			if len(needsPush) > 0 {
				mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
				normalStyle := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg)
				var repoLines []string
				for _, up := range needsPush {
					status := "not yet pushed"
					if up.count > 0 {
						status = fmt.Sprintf("%d unpushed commit(s)", up.count)
					}
					repoLines = append(repoLines, fmt.Sprintf("  %s %s %s",
						mutedStyle.Render(up.rc.repo.Name),
						mutedStyle.Render(ui.Palette.Dot),
						normalStyle.Render(status)))
				}

				promptContent := mutedStyle.Render("Do you want to push before continuing?") +
					"\n\n" + strings.Join(repoLines, "\n")

				slot := ui.NewSlot()
				slot.Show(ui.NewCard(ui.CardInput, "unpushed commits detected").Tight())

				confirmed := true
				if isInteractive() {
					if err := runForm(
						newConfirm().
							Title(promptContent).
							Affirmative("Yes").
							Negative("No").
							Value(&confirmed),
					); err != nil {
						return err
					}
				}
				if !confirmed {
					slot.Show(ui.NewCard(ui.CardSkipped, "push declined"))
					slot.Finalize()
					return fmt.Errorf("aborted: unpushed commits")
				}

				for _, up := range needsPush {
					if err := slot.Run(fmt.Sprintf("pushing %s", up.rc.repo.Name), func() error {
						return gitClient.Push(ctx, up.rc.repo.Path, up.rc.branch)
					}); err != nil {
						return fmt.Errorf("pushing %s: %w", up.rc.repo.Name, err)
					}
				}
				pushedPairs := make([]string, 0, len(needsPush)*2)
				for _, up := range needsPush {
					pushedPairs = append(pushedPairs, up.rc.repo.Name, up.rc.branch)
				}
				slot.Show(ui.NewCard(ui.CardSuccess, "pushed").KV(pushedPairs...))
				slot.Finalize()
			}

			// --- Plan + Apply ---

			var actions []Action

			type prResult struct {
				repo     string
				pr       code.PullRequest
				owner    string
				repoName string
				branch   string
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
						Action: "pr",
						Type:   "repo",
						Name:   repoDisplayName,
						Assess: func(ctx context.Context) (ActionState, string, error) {
							existing, err := host.GetPRForBranch(ctx, owner, repoName, branch)
							if err != nil {
								return 0, "", err
							}
							if existing.Number > 0 {
								// Capture existing PR so notifications have data
								// even when no new PRs are created.
								prResults = append(prResults, prResult{
									repo: repoDisplayName, pr: existing,
									owner: owner, repoName: repoName, branch: branch,
								})
								return ActionCompleted, fmt.Sprintf("#%d", existing.Number), nil
							}
							detail := fmt.Sprintf("%s → %s", branch, baseBranch)
							if draft {
								detail += " (draft)"
							}
							return ActionNeeded, detail, nil
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
								owner: owner, repoName: repoName, branch: branch,
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

			// Notification action — appears in plan, runs after PR creation.
			reviewChannel := viper.GetString("slack.channel_review")
			notifier, notifierErr := newNotifier()
			if notifierErr == nil {
				defer notifier.Close()
			}

			// Resolve GitHub avatar for card icons.
			var avatarURL string
			if host != nil {
				if user, err := host.GetAuthenticatedUser(ctx); err == nil {
					avatarURL = fmt.Sprintf("https://github.com/%s.png?size=36", user)
				}
			}

			if !draft && reviewChannel != "" && notifierErr == nil {
				notifyOp := ui.PlanCreate
				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					OpRef:  &notifyOp,
					Action: "notify",
					Type:   "channel",
					Name:   reviewChannel,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						ref, _ := notifier.FindThread(ctx, reviewChannel, issue)
						if ref.Timestamp == "" {
							return ActionNeeded, "new notification", nil
						}
						// Check if content has changed by comparing hashes.
						items := make([]notify.Item, len(prResults))
						for i, r := range prResults {
							items[i] = notify.Item{
								Label:     r.repo,
								URL:       r.pr.URL,
								Detail:    fmt.Sprintf("#%d", r.pr.Number),
								Body:      r.pr.Body,
								BranchURL: fmt.Sprintf("https://github.com/%s/%s/tree/%s", r.owner, r.repoName, r.branch),
							}
						}
						content := buildNotifyContent("review", notifyTemplateData{
							IssueKey:   issue,
							IssueTitle: detail.Title,
							IssueType:  detail.Type,
							IssueURL:   detail.URL,
							IconURL:    avatarURL,
							Items:      items,
						})
						hash := notify.ContentHash(content)
						if ref.ContentHash == hash {
							notifyOp = ui.PlanModify
							return ActionCompleted, "notification unchanged", nil
						}
						notifyOp = ui.PlanModify
						return ActionNeeded, "update notification", nil
					},
					Apply: func(ctx context.Context) error {
						if len(prResults) == 0 {
							return nil
						}
						// Request reviewers and add assignees (best-effort).
						if host != nil {
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
						items := make([]notify.Item, len(prResults))
						for i, r := range prResults {
							items[i] = notify.Item{
								Label:     r.repo,
								URL:       r.pr.URL,
								Detail:    fmt.Sprintf("#%d", r.pr.Number),
								Body:      r.pr.Body,
								BranchURL: fmt.Sprintf("https://github.com/%s/%s/tree/%s", r.owner, r.repoName, r.branch),
							}
						}
						_, err := notifier.Notify(ctx, notify.Message{
							Channel:  reviewChannel,
							IssueKey: issue,
							Title:    detail.Title,
							IssueURL: detail.URL,
							Items:    items,
							Content: buildNotifyContent("review", notifyTemplateData{
								IssueKey:   issue,
								IssueTitle: detail.Title,
								IssueType:  detail.Type,
								IssueURL:   detail.URL,
								IconURL:    avatarURL,
								Items:      items,
							}),
						})
						return err
					},
				})
			}

			if err := runActions(cmd, ctx, actions); err != nil {
				return err
			}

			return nil
		},
	}

	addIssueFlag(cmd)
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	cmd.Flags().Bool("draft", false, "create draft pull request(s), skip status update and notifications")
	cmd.Flags().String("base", "", "target branch (default: pull_request.base config or main)")
	cmd.Flags().String("title", "", "override PR title")
	cmd.Flags().String("body", "", "override PR body")
	cmd.Flags().StringSlice("reviewer", nil, "request review from user (repeatable)")
	cmd.Flags().StringSlice("team-reviewer", nil, "request review from team (repeatable)")
	cmd.Flags().StringSlice("assignee", nil, "assign PR to user (repeatable)")
	cmd.Flags().Bool("self-assign", false, "assign PR to yourself")
	return cmd
}
