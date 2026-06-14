package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// prState carries per-repo PR discoveries from a pr-action's Assess
// to its Apply: the existing PR (zero value when none) and the
// per-axis deltas — reviewers/teams/assignees that are configured
// but not yet on the PR.
type prState struct {
	pr           code.PullRequest
	missingRevs  []string
	missingTeams []string
	missingAsns  []string
}

// diffCaseInsensitive returns elements of want that don't appear in
// have, comparing case-insensitively but preserving want's original
// casing in the result (so GitHub's API gets the value the user
// configured, not a lowercased echo of theirs).
func diffCaseInsensitive(want, have []string) []string {
	if len(want) == 0 {
		return nil
	}
	haveSet := make(map[string]struct{}, len(have))
	for _, h := range have {
		haveSet[strings.ToLower(h)] = struct{}{}
	}
	var out []string
	for _, w := range want {
		if _, ok := haveSet[strings.ToLower(w)]; !ok {
			out = append(out, w)
		}
	}
	return out
}

func newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Submit issue for code review",
		Annotations: map[string]string{
			headerAnnotationTitle: "submit for review",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cc := commandContext(cmd)
			if err := cc.RequireWorkspace(); err != nil {
				return err
			}
			if err := cc.RequireIssue(); err != nil {
				return err
			}
			issue := cc.Issue

			ctx := cmd.Context()
			draft, _ := cmd.Flags().GetBool("draft")

			// --- Resolve ---

			detail := emitLifecyclePreamble(ctx, issue)

			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			repositories, err := resolveActiveRepositories(ctx, cc.Workspace, filterRepositories)
			if err != nil {
				return err
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("code host: %v", hostErr))
			}

			// --- Pre-flight: Workspace Readiness + repo identities ---
			//
			// Shared readiness section (same as preview/release): one
			// card per repo with the push offer + dirty gate folded in.
			// Pushing is optional here too, but with a review-specific
			// consequence — a never-pushed branch has no remote head
			// ref, so it can't be the base of a PR. Those repos drop out
			// of PR creation with a note; pushed-but-behind repos open
			// PRs that simply lag their local commits.

			gitClient := git.New()

			type repoContext struct {
				repo     Repository
				branch   string
				owner    string
				repoName string
			}
			var resolved []repoContext

			readiness, _, anyUnpushed, err := gatherRepoReadiness(ctx, gitClient, repositories)
			if err != nil {
				return err
			}
			if err := emitWorkspaceReadiness(ctx, gitClient, readiness, anyUnpushed); err != nil {
				return err // ErrCancelled (dirty gate) propagates as a clean abort
			}

			// Resolve identities for the repos that can actually be
			// reviewed. A never-pushed-and-not-pushed branch is skipped:
			// GitHub has no head ref to open a PR against.
			for i := range readiness {
				rr := &readiness[i]
				if !rr.hasRemoteBranch() {
					ui.SkipValue(ui.PreserveCase(rr.repo.Name), "not pushed — no remote branch to review")
					continue
				}
				identity, err := gh.ParseRemote(ctx, rr.repo.Path)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: %v", rr.repo.Name, err))
					continue
				}
				resolved = append(resolved, repoContext{
					repo: rr.repo, branch: rr.branch,
					owner: identity.Owner, repoName: identity.Name,
				})
			}

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

					// prOp switches PlanCreate↔PlanModify based on what
					// Assess discovers: create when the PR is missing,
					// modify when it exists but reviewers/teams/assignees
					// need filling in.
					prOp := ui.PlanCreate

					// state carries the per-repo discoveries from Assess
					// into Apply. The pr field is non-zero only when the
					// PR already existed (and we don't need to create
					// it); the missing* fields are the deltas to apply
					// regardless of which path Assess took.
					state := &prState{}

					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						OpRef:  &prOp,
						Action: "pr",
						Type:   "repo",
						Name:   repoDisplayName,
						Assess: func(ctx context.Context) (ActionState, string, error) {
							existing, err := host.GetPRForBranch(ctx, owner, repoName, branch)
							if err != nil {
								return 0, "", err
							}

							if existing.Number == 0 {
								// PR doesn't exist — Apply will create
								// it and apply all requested
								// reviewers/teams/assignees fresh.
								state.missingRevs = reviewers
								state.missingTeams = teamReviewers
								state.missingAsns = assignees
								detail := fmt.Sprintf("%s → %s", branch, baseBranch)
								if draft {
									detail += " (draft)"
								}
								prOp = ui.PlanCreate
								return ActionNeeded, detail, nil
							}

							// PR exists — capture it for downstream
							// (notify needs prResults) and compute the
							// reviewer/team/assignee deltas.
							state.pr = existing
							prResults = append(prResults, prResult{
								repo: repoDisplayName, pr: existing,
								owner: owner, repoName: repoName, branch: branch,
							})

							state.missingRevs = diffCaseInsensitive(reviewers, existing.RequestedReviewers)
							state.missingTeams = diffCaseInsensitive(teamReviewers, existing.RequestedTeams)
							state.missingAsns = diffCaseInsensitive(assignees, existing.Assignees)

							total := len(state.missingRevs) + len(state.missingTeams) + len(state.missingAsns)
							if total == 0 {
								return ActionCompleted, fmt.Sprintf("#%d", existing.Number), nil
							}
							prOp = ui.PlanModify
							return ActionNeeded, fmt.Sprintf("#%d (+%d)", existing.Number, total), nil
						},
						Apply: func(ctx context.Context) error {
							pr := state.pr
							if pr.Number == 0 {
								created, err := host.CreatePR(ctx, code.CreatePRRequest{
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
								pr = created
								prResults = append(prResults, prResult{
									repo: repoDisplayName, pr: created,
									owner: owner, repoName: repoName, branch: branch,
								})
							}

							// Reviewers/assignees are best-effort: the PR
							// is already created (or pre-existed), so a
							// failure here shouldn't abort the rest of
							// the run. Surface failures as Fail cards
							// and continue.
							if len(state.missingRevs) > 0 || len(state.missingTeams) > 0 {
								if err := host.RequestReviewers(ctx, owner, repoName, pr.Number, state.missingRevs, state.missingTeams); err != nil {
									ui.Fail(fmt.Sprintf("%s: reviewers: %v", repoDisplayName, err))
								}
							}
							if len(state.missingAsns) > 0 {
								if err := host.AddAssignees(ctx, owner, repoName, pr.Number, state.missingAsns); err != nil {
									ui.Fail(fmt.Sprintf("%s: assignees: %v", repoDisplayName, err))
								}
							}
							return nil
						},
					})
				}
			}

			if !draft {
				tracker, _ := newIssueTracker()
				if sa, ok := statusAction(tracker, issue, detail.Status, "review"); ok {
					actions = append(actions, sa)
				}
			}

			// Notification action — appears in plan, runs after PR creation.
			reviewChannel := viper.GetString("notification.channel_review")
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

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
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
