package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// repoContext is one workspace repo resolved for review: its remote
// identity, the branch under review, the PR currently associated with
// that branch (zero value when none), and whether the user selected it
// for PR creation. pr/prErr/include are filled by selectReviewTargets;
// the rest by the identity-resolution loop.
type repoContext struct {
	repo     Repository
	branch   string
	owner    string
	repoName string

	pr      code.PullRequest // existing PR for branch (zero value = none)
	prErr   error            // lookup failure, surfaced as a ✗ plan row
	include bool             // selected for PR creation (creatable repos)
}

// activePR reports whether rc's branch already has an open or draft PR
// — the only state that takes the in-place "update reviewers" path
// rather than creating a fresh PR.
func (rc *repoContext) activePR() bool {
	return rc.pr.Number > 0 &&
		(rc.pr.State == "open" || rc.pr.State == "draft")
}

// creatable reports whether rc is a candidate for PR creation: its
// lookup succeeded and it has no active PR (none, or only a
// closed/merged one). These are the repos the selection form offers.
func (rc *repoContext) creatable() bool {
	return rc.prErr == nil && !rc.activePR()
}

// preselect reports whether a creatable repo should be pre-checked in
// the selection form (and included by default when no form shows). A
// merged PR is the opt-in case — its work already landed, so creating a
// fresh PR is left unchecked; everything else creatable pre-checks.
func (rc *repoContext) preselect() bool {
	return rc.creatable() && rc.pr.State != "merged"
}

// statusNote returns a "#N (state)" descriptor for the repo's existing PR,
// or "" when no PR exists. Shared by the selection form and the result
// card so the two surfaces describe each repo identically — the checkbox
// (form) and glyph (card) convey the action; the plan is the single
// source of truth for what create/update/no-op each row resolves to.
func (rc *repoContext) statusNote() string {
	if rc.pr.Number == 0 {
		return ""
	}
	return fmt.Sprintf("#%d (%s)", rc.pr.Number, rc.pr.State)
}

// selectReviewTargets fetches each repo's PR status under a spinner
// sequence, then — when interactive — offers a multi-select of the
// creatable repos so the user can choose which get a PR created. Repos
// with an active PR aren't offered (they're updated in place); merged
// repos appear unchecked. Mutates resolved in place: sets pr/prErr on
// every repo and include on the creatable ones.
//
// Either way the section ends with a static "Pull Requests" card
// recording the outcome per repo (create / update / skipped) — the
// spinner program morphs into the form header when the form shows and
// into that card otherwise, and the submitted form is replaced by it.
func selectReviewTargets(ctx context.Context, cmd *cobra.Command, host code.Host, resolved []repoContext) error {
	if host == nil || len(resolved) == 0 {
		return nil
	}

	// --- Phase 1: PR status lookup, one program over all repos ---

	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	steps := make([]ui.CardStep, 0, len(resolved))
	for i := range resolved {
		rc := &resolved[i]
		spin := ui.NewCard(ui.CardRunning, "pull requests").
			Raw(statusMuted.Render("Checking ") +
				ui.Keyword(rc.repo.Name) +
				statusMuted.Render("..."))
		steps = append(steps, ui.CardStep{
			Card: spin,
			// Per-repo errors ride on prErr (surfaced as a ✗ plan row),
			// not the step error path, so the sequence finishes.
			Run: func() error {
				rc.pr, rc.prErr = host.GetPRForBranch(ctx, rc.owner, rc.repoName, rc.branch)
				return nil
			},
		})
	}

	formGate := func() bool {
		if !isInteractive() || isAutoApprove(cmd) {
			return false
		}
		for i := range resolved {
			if resolved[i].creatable() || resolved[i].activePR() {
				return true
			}
		}
		return false
	}

	// applyDefaults seeds include whenever no form shows (non-interactive,
	// -y, or no selectable repo). Passing --repository is an explicit
	// "operate on these repos" — every resolved repo (already narrowed to
	// the named set upstream) is acted on, so non-interactive runs can
	// update existing open PRs. Without it, fall back to the pre-selection
	// policy: create missing PRs, leave open PRs untouched.
	explicitRepos := cmd.Flags().Changed("repository")
	applyDefaults := func() {
		for i := range resolved {
			resolved[i].include = explicitRepos || resolved[i].preselect()
		}
	}

	// The spinner program's final frame morphs into the selection form's
	// header when the form will show, and into the result card
	// otherwise (so the no-form path lands its record without a flash).
	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if formGate() {
			return ui.NewCard(ui.CardInput, "pull requests").Tight()
		}
		applyDefaults()
		return buildReviewTargetsCard(resolved)
	})
	if err != nil {
		return err
	}

	if !formGate() {
		// No form — the successor card already recorded the outcome
		// (except in raw mode, which skips it); re-apply defaults so the
		// action loop sees them there too.
		applyDefaults()
		return nil
	}

	// --- Phase 2: selection form over creatable repos ---

	var idxs []int
	for i := range resolved {
		if resolved[i].creatable() || resolved[i].activePR() {
			idxs = append(idxs, i)
		}
	}
	sort.SliceStable(idxs, func(a, b int) bool {
		return resolved[idxs[a]].repo.Name < resolved[idxs[b]].repo.Name
	})

	// Bold the repo segment (raw SGR 1/22, not lipgloss, so huh's own
	// selection styling for the rest of the line survives — lipgloss
	// would close with a full reset). Each row shows only the repo's PR
	// status (#N (state), or nothing when none); the checkbox conveys
	// whether we act on it, and the plan spells out create vs. update.
	opts := make([]huh.Option[string], len(idxs))
	for j, i := range idxs {
		rc := &resolved[i]
		label := "\x1b[1m" + rc.repo.Name + "\x1b[22m"
		if note := rc.statusNote(); note != "" {
			label += " · " + note
		}
		opts[j] = huh.NewOption(label, strconv.Itoa(i)).Selected(rc.preselect())
	}

	var picked []string
	// The header was painted by the spinner program's final frame (not
	// via Print), so suppress the spacer the way Tight-on-Print would.
	ui.ClearSpacer()
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(opts...).
			Height(len(opts)).
			Value(&picked),
	); err != nil {
		ui.RequestSpacer()
		return err
	}

	pickedSet := make(map[int]bool, len(picked))
	for _, p := range picked {
		if i, err := strconv.Atoi(p); err == nil {
			pickedSet[i] = true
		}
	}
	for _, i := range idxs {
		resolved[i].include = pickedSet[i]
	}

	// Erase the form header and drop the result card in its place — the
	// same in-place swap preview uses for its Services card.
	rewind()
	buildReviewTargetsCard(resolved).Print()
	return nil
}

// buildReviewTargetsCard composes the static "Pull Requests" card: one
// row per PR-able repo. Each row mirrors its selection-form counterpart —
// the repo plus its PR status (#N (state), or nothing when none) — with
// the glyph carrying whether the repo is acted on (✓) or skipped (○). The
// plan that follows is the single source for create vs. update vs. no-op.
// The card glyph aggregates worst-first (fail > skipped > success) the way
// group parents aggregate children.
func buildReviewTargetsCard(resolved []repoContext) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphOff := muted.Render("○")
	glyphFail := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render("✗")

	type row struct {
		repo, glyph, content string
	}
	var rows []row
	var nOK, nSkip, nFail int

	// on: included rows keep the repo in primary color; off: skipped rows
	// recede entirely (repo segment drops to muted too). A blank note
	// (repo with no PR) renders the repo name alone, no trailing " · ".
	on := func(name, note string) string {
		if note == "" {
			return repoStyle.Render(name)
		}
		return repoStyle.Render(name) + muted.Render(" · "+note)
	}
	off := func(name, note string) string {
		if note == "" {
			return muted.Render(name)
		}
		return muted.Render(name + " · " + note)
	}

	for i := range resolved {
		rc := &resolved[i]
		name := rc.repo.Name
		switch {
		case rc.prErr != nil:
			nFail++
			rows = append(rows, row{name, glyphFail, on(name, rc.prErr.Error())})
		case rc.include:
			nOK++
			rows = append(rows, row{name, glyphOK, on(name, rc.statusNote())})
		default:
			nSkip++
			rows = append(rows, row{name, glyphOff, off(name, rc.statusNote())})
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].repo < rows[j].repo })

	state := ui.CardSuccess
	switch {
	case nFail > 0:
		state = ui.CardFailed
	case nSkip > 0 && nOK == 0:
		state = ui.CardSkipped
	}

	card := ui.NewCard(state, "pull requests")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
	}
	return card
}

// prState carries per-repo PR discoveries from a pr-action's Assess
// to its Apply: the existing PR (zero value when none) and the
// per-axis deltas to apply.
//
// For a create, add* hold the full requested sets and remove* are empty.
// For an in-place update, add*/remove* reconcile the PR's reviewers,
// teams, and assignees to exactly the requested state, and
// contentChanged (with title/body/base) drives an EditPR overwrite.
type prState struct {
	pr code.PullRequest

	addRevs     []string
	addTeams    []string
	addAsns     []string
	removeRevs  []string
	removeTeams []string
	removeAsns  []string

	contentChanged bool
	title          string
	body           string
	base           string
}

// planSync computes the deltas needed to reconcile an existing PR to the
// requested state: reviewers/teams/assignees to add and remove, plus
// whether title/body/base differ and must be overwritten. The result's
// remove* sets are empty for a fresh create (callers pass that path their
// own full add* sets directly).
func planSync(existing code.PullRequest, reviewers, teamReviewers, assignees []string, title, body, base string) *prState {
	return &prState{
		pr:          existing,
		addRevs:     diffCaseInsensitive(reviewers, existing.RequestedReviewers),
		removeRevs:  diffCaseInsensitive(existing.RequestedReviewers, reviewers),
		addTeams:    diffCaseInsensitive(teamReviewers, existing.RequestedTeams),
		removeTeams: diffCaseInsensitive(existing.RequestedTeams, teamReviewers),
		addAsns:     diffCaseInsensitive(assignees, existing.Assignees),
		removeAsns:  diffCaseInsensitive(existing.Assignees, assignees),
		title:       title,
		body:        body,
		base:        base,
		contentChanged: existing.Title != title ||
			existing.Body != body ||
			existing.BaseRef != base,
	}
}

// hasChanges reports whether a sync has any delta to apply.
func (s *prState) hasChanges() bool {
	return s.contentChanged ||
		len(s.addRevs)+len(s.removeRevs)+
			len(s.addTeams)+len(s.removeTeams)+
			len(s.addAsns)+len(s.removeAsns) > 0
}

// syncSummary renders a compact delta summary for an in-place PR update,
// e.g. "content +2/-1 rev +1 asn".
func syncSummary(s *prState) string {
	var parts []string
	if s.contentChanged {
		parts = append(parts, "content")
	}
	if a, r := len(s.addRevs)+len(s.addTeams), len(s.removeRevs)+len(s.removeTeams); a+r > 0 {
		parts = append(parts, countPair(a, r)+" rev")
	}
	if a, r := len(s.addAsns), len(s.removeAsns); a+r > 0 {
		parts = append(parts, countPair(a, r)+" asn")
	}
	return strings.Join(parts, " ")
}

// countPair formats an add/remove pair, omitting a zero side.
func countPair(add, rem int) string {
	switch {
	case add > 0 && rem > 0:
		return fmt.Sprintf("+%d/-%d", add, rem)
	case rem > 0:
		return fmt.Sprintf("-%d", rem)
	default:
		return fmt.Sprintf("+%d", add)
	}
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

			var resolved []repoContext

			readiness, _, err := emitWorkspaceReadiness(ctx, gitClient, repositories)
			if err != nil {
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

			// --- Observe + select: which repos get a PR created ---
			//
			// Look up each branch's PR status, then (interactively) let
			// the user pick which repos to open a PR for, pre-checking
			// the ones that should have one. Repos with an active PR are
			// updated in place; merged repos are unchecked by default.
			if err := selectReviewTargets(ctx, cmd, host, resolved); err != nil {
				return err // ErrCancelled (form abort) propagates as a clean abort
			}

			// --- Resolve PR metadata from flags, config, and interactive prompts ---

			baseBranch, _ := cmd.Flags().GetString("base")
			if baseBranch == "" {
				baseBranch = viper.GetString("pull_request.base")
			}
			if baseBranch == "" {
				baseBranch = "main"
			}
			// Open-ended text input (current value as placeholder, Enter
			// accepts it). A single base applies to every repo's PR — see
			// the deferred "per-repo PR metadata" overhaul for the
			// mixed-base / per-repo-branch-list case this intentionally
			// doesn't cover yet.
			if forceInteractive(cmd) && !cmd.Flags().Changed("base") {
				val, err := typeaheadInput("Base Branch", baseBranch)
				if err != nil {
					return err
				}
				baseBranch = val
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

			// Resolve the authenticated user once; reused to exclude the
			// author from the reviewer list (GitHub forbids self-review),
			// float them to the top of the assignee list, and honor
			// --self-assign below.
			var selfUser string
			if host != nil {
				if u, err := host.GetAuthenticatedUser(ctx); err != nil {
					ui.Fail(fmt.Sprintf("authenticated user: %v", err))
				} else {
					selfUser = u
				}
			}

			// Resolve reviewers and assignees from config + flags.
			reviewers := viper.GetStringSlice("pull_request.reviewers")
			if flagReviewers, _ := cmd.Flags().GetStringSlice("reviewer"); len(flagReviewers) > 0 {
				reviewers = append(reviewers, flagReviewers...)
			}
			if forceInteractive(cmd) && !cmd.Flags().Changed("reviewer") && host != nil {
				selected, err := typeaheadMultiSelect("Reviewers", reviewers, func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				}, excludeUser(selfUser))
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
			if selfAssign && selfUser != "" {
				duplicate := false
				for _, a := range assignees {
					if strings.EqualFold(a, selfUser) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					assignees = append(assignees, selfUser)
				}
			}

			if forceInteractive(cmd) && !cmd.Flags().Changed("assignee") && host != nil {
				selected, err := typeaheadMultiSelect("Assignees", assignees, func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				}, promoteUser(selfUser))
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

					// PR status pre-fetched by selectReviewTargets, along
					// with whether the user selected this repo for
					// creation.
					existing := rc.pr
					prErr := rc.prErr
					include := rc.include

					// prOp switches between PlanCreate (selected repo with
					// no active PR) and PlanModify (open PR needing
					// reviewers/teams/assignees filled in).
					prOp := ui.PlanCreate

					// state carries the per-repo discoveries from Assess
					// into Apply. The pr field is non-zero only when the
					// PR already existed (and we don't need to create
					// it); the add*/remove*/content fields are the deltas
					// to apply regardless of which path Assess took.
					state := &prState{}

					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						OpRef:  &prOp,
						Action: "pr",
						Type:   "repo",
						Name:   repoDisplayName,
						Assess: func(ctx context.Context) (ActionState, string, error) {
							if prErr != nil {
								return 0, "", prErr
							}

							// An open/draft PR is a candidate for in-place
							// update; anything else (none, closed, merged) is
							// a creation candidate. Both are gated by the
							// user's selection.
							active := existing.Number > 0 &&
								(existing.State == "open" || existing.State == "draft")

							// Every open/draft PR in the workspace is recorded
							// for the notification, whether or not we mutate
							// it. When we will update it, the record carries
							// the desired title/body/base so the notification
							// reflects the post-update state.
							if active {
								rec := existing
								if include {
									rec.Title = prTitle
									rec.Body = prBody
									rec.BaseRef = baseBranch
								}
								prResults = append(prResults, prResult{
									repo: repoDisplayName, pr: rec,
									owner: owner, repoName: repoName, branch: branch,
								})
							}

							if active {
								if !include {
									// Left unchecked — report the open PR,
									// touch nothing.
									return ActionCompleted, fmt.Sprintf("#%d", existing.Number), nil
								}

								// Checked for update — reconcile reviewers,
								// teams, and assignees to exactly the requested
								// state (add missing, remove unwanted) and
								// overwrite title/body/base when they differ.
								*state = *planSync(existing, reviewers, teamReviewers, assignees, prTitle, prBody, baseBranch)
								if !state.hasChanges() {
									return ActionCompleted, fmt.Sprintf("#%d", existing.Number), nil
								}
								prOp = ui.PlanModify
								return ActionNeeded, fmt.Sprintf("#%d %s", existing.Number, syncSummary(state)), nil
							}

							// No active PR. Creatable but left out — show an
							// unchanged row so the plan still covers every
							// repo. A prior closed/merged PR is named so the
							// no-op reads as intentional rather than an
							// oversight.
							if !include {
								if existing.Number > 0 {
									return ActionCompleted, fmt.Sprintf("#%d %s", existing.Number, existing.State), nil
								}
								return ActionCompleted, "not selected", nil
							}

							// Selected for creation — Apply opens a new PR
							// and applies all requested reviewers/teams/
							// assignees fresh.
							state.addRevs = reviewers
							state.addTeams = teamReviewers
							state.addAsns = assignees
							detail := fmt.Sprintf("%s → %s", branch, baseBranch)
							if draft {
								detail += " (draft)"
							}
							prOp = ui.PlanCreate
							return ActionNeeded, detail, nil
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
							} else if state.contentChanged {
								// In-place update: overwrite title/body/base.
								if err := host.EditPR(ctx, code.EditPRRequest{
									Owner:      owner,
									Repository: repoName,
									Number:     pr.Number,
									Title:      state.title,
									Body:       state.body,
									Base:       state.base,
								}); err != nil {
									ui.Fail(fmt.Sprintf("%s: edit pr: %v", repoDisplayName, err))
								}
							}

							// Reviewer/assignee writes are best-effort: the PR
							// already exists, so a failure here shouldn't
							// abort the rest of the run. Surface failures as
							// Fail cards and continue.
							if len(state.addRevs) > 0 || len(state.addTeams) > 0 {
								if err := host.RequestReviewers(ctx, owner, repoName, pr.Number, state.addRevs, state.addTeams); err != nil {
									ui.Fail(fmt.Sprintf("%s: reviewers: %v", repoDisplayName, err))
								}
							}
							if len(state.removeRevs) > 0 || len(state.removeTeams) > 0 {
								if err := host.RemoveReviewers(ctx, owner, repoName, pr.Number, state.removeRevs, state.removeTeams); err != nil {
									ui.Fail(fmt.Sprintf("%s: remove reviewers: %v", repoDisplayName, err))
								}
							}
							if len(state.addAsns) > 0 {
								if err := host.AddAssignees(ctx, owner, repoName, pr.Number, state.addAsns); err != nil {
									ui.Fail(fmt.Sprintf("%s: assignees: %v", repoDisplayName, err))
								}
							}
							if len(state.removeAsns) > 0 {
								if err := host.RemoveAssignees(ctx, owner, repoName, pr.Number, state.removeAsns); err != nil {
									ui.Fail(fmt.Sprintf("%s: remove assignees: %v", repoDisplayName, err))
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

			// notifyItems renders every open PR in the workspace (existing
			// or freshly created) as a notification row, sorted by repo so
			// the content — and thus its hash — is stable across runs
			// regardless of the order PRs were discovered or applied.
			notifyItems := func() []notify.Item {
				sort.Slice(prResults, func(i, j int) bool {
					return prResults[i].repo < prResults[j].repo
				})
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
				return items
			}

			// willCreate reports whether this run opens a new PR. A created
			// PR isn't in prResults until Apply, so the notify Assess can't
			// hash it in — force a refresh instead of risking a stale "no
			// change" when the pre-existing set's hash happens to match.
			willCreate := func() bool {
				for i := range resolved {
					if resolved[i].include && resolved[i].creatable() {
						return true
					}
				}
				return false
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
						if willCreate() {
							notifyOp = ui.PlanModify
							return ActionNeeded, "update notification", nil
						}
						// No new PR this run, so prResults already holds the
						// full open-PR set — compare hashes to detect a real
						// change.
						content := buildNotifyContent("review", notifyTemplateData{
							IssueKey:   issue,
							IssueTitle: detail.Title,
							IssueType:  detail.Type,
							IssueURL:   detail.URL,
							IconURL:    avatarURL,
							Items:      notifyItems(),
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
						items := notifyItems()
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
