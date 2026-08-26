package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// repoContext is one workspace repo resolved for review: its remote
// identity, the branch under review, the PR currently associated with
// that branch (zero value when none), and whether the user selected it
// for PR creation. pr/prErr/include/defaultBranch are filled by
// selectReviewTargets; meta by the metadata resolution that follows;
// the rest by the identity-resolution loop.
type repoContext struct {
	repo     Repository
	branch   string
	owner    string
	repoName string

	pr            code.PullRequest // existing PR for branch (zero value = none)
	prErr         error            // lookup failure, surfaced as a ✗ plan row
	include       bool             // selected for PR creation (creatable repos)
	defaultBranch string           // repo's own default branch (base fallback)

	// cfg is this repository's effective configuration — its own
	// committed .bosun.yaml over the central layers. Loaded from the
	// WORKTREE path, so PR policy is read from the branch under review
	// rather than from whatever the main checkout happens to be on.
	cfg repoConfig

	meta prMetadata // the PR content this repo is created/synced with
}

// prConfig returns rc's effective configuration, nil-safe because the
// shared prompt pass reads it through the representative repo, which is
// nil when this run has nothing writable. A nil receiver answers from
// the central layers, which is all a run with no PR to open can show.
func (rc *repoContext) prConfig() repoConfig {
	if rc == nil {
		return repoConfig{}
	}
	return rc.cfg
}

// nonNilSlice returns s, or an empty non-nil slice when s is nil.
//
// It exists for one distinction the shared prompt pass depends on: nil
// means "no prompt has spoken for this list", while empty means "the
// user was asked and cleared it". Without it, deselecting every
// reviewer would read as unanswered and each repo would fall back to
// its configured list — silently re-adding the reviewers the user had
// just removed.
func nonNilSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// The repo-scoped PR policy keys. Named once because each is now read
// through repoConfig rather than viper, and a typo in one of them would
// silently resolve to "nothing configured" — the same failure the
// unknown-key check exists to catch, but on the reading side.
const (
	prBaseKey          = "pull_request.base"
	prReviewersKey     = "pull_request.reviewers"
	prTeamReviewersKey = "pull_request.team_reviewers"
	prAssigneesKey     = "pull_request.assignees"
)

// prMetadata is the PR content applied to ONE repository. Every repo
// starts from the shared resolution (flags, config, the shared prompt
// pass) with base defaulting to that repo's own default branch, and can
// then be overridden individually in the customization pass. Keeping it
// per repo is the point: a workspace-wide base that doesn't exist in
// repo N fails N's CreatePR, and reviewer lists drawn from one repo's
// collaborators don't necessarily resolve on the others.
type prMetadata struct {
	base      string
	title     string
	body      string
	reviewers []string
	teams     []string
	assignees []string
}

// defaultBaseBranch is the last-resort PR base: neither --base nor
// config named one, and neither the host nor the local clone could say
// what the repo actually targets.
const defaultBaseBranch = "main"

// baseBranch returns the branch rc's PR targets when no --base override
// applies: this repository's configured base, else its own default
// branch, falling back to defaultBaseBranch when neither resolves.
//
// The configured base sits here rather than in the caller because it is
// now repo-scoped — a repository's descriptor may name a base its
// siblings don't have. Reading it per repo is also what lets the shared
// Base Branch prompt tell "every repo agrees" from "each repo differs"
// instead of showing one central literal as though it applied to all.
func (rc *repoContext) baseBranch() string {
	if b := rc.cfg.String(prBaseKey); b != "" {
		return b
	}
	if rc.defaultBranch != "" {
		return rc.defaultBranch
	}
	return defaultBaseBranch
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

// selectReviewTargets fetches each repo's PR status and default branch
// under a spinner sequence, then — when interactive — offers a
// multi-select of the creatable repos so the user can choose which get
// a PR created. Repos with an active PR aren't offered (they're updated
// in place); merged repos appear unchecked. Mutates resolved in place:
// sets pr/prErr/defaultBranch on every repo and include on the
// creatable ones.
//
// Either way the section ends with a static "Pull Requests" card
// recording the outcome per repo (create / update / skipped) — the
// spinner program morphs into the form header when the form shows and
// into that card otherwise, and the submitted form is replaced by it.
func selectReviewTargets(ctx context.Context, cmd *cobra.Command, host code.Host, g vcs.VCS, resolved []repoContext) error {
	if host == nil || len(resolved) == 0 {
		return nil
	}

	// --- Phase 1: PR status lookup, one program over all repos ---
	//
	// Accumulating gather (the release-command pattern): each step's
	// card carries the repo rows resolved so far plus a pending row —
	// the in-flight repo with the live spinner in its glyph slot — so
	// the list materializes in place and then swaps into the form (or
	// record card). Rows render via reviewTargetRow, the record card's
	// own renderer. Mutating later cards from a step's Run is race-free
	// (RunCardSteps' channel happens-before; see release_gate.go).

	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	n := len(resolved)
	pendingRow := func(c *ui.Card, i int) {
		c.Item(ui.GlyphSlot, statusMuted.Render(resolved[i].repo.Name))
	}
	cards := make([]*ui.Card, n)
	for i := range cards {
		cards[i] = ui.NewCard(ui.CardRunning, "pull requests")
	}
	pendingRow(cards[0], 0)

	steps := make([]ui.CardStep, 0, n)
	for i := range resolved {
		rc := &resolved[i]
		steps = append(steps, ui.CardStep{
			Card: cards[i],
			// Per-repo errors ride on prErr (surfaced as a ✗ plan row),
			// not the step error path, so the sequence finishes.
			Run: func() error {
				rc.pr, rc.prErr = host.GetPRForBranch(ctx, rc.owner, rc.repoName, rc.branch)
				rc.defaultBranch = resolveDefaultBranch(ctx, host, g, rc)
				glyph, content := reviewTargetRow(rc, rc.preselect())
				for j := i + 1; j < n; j++ {
					cards[j].Item(glyph, content)
				}
				if i+1 < n {
					pendingRow(cards[i+1], i+1)
				}
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

	// --- Phase 2: selection form over creatable repos ---
	//
	// Built inside a closure because the steps program's final frame
	// paints the form header plus the form's EXACT first frame
	// (formFirstFrame — same construction as runForm), so the takeover
	// below repaints identical bytes and the gather → form transition
	// has no collapse and no flash.

	var (
		picked      []string
		msField     *huh.MultiSelect[string]
		formFrame   string
		headerLines int
		idxs        []int
	)
	buildSelectionForm := func() {
		if msField != nil {
			return
		}
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
		// Full height whenever it fits, capped to the terminal: a frame
		// taller than the screen breaks the takeover below rather than
		// just overflowing.
		msField = huh.NewMultiSelect[string]().
			Options(opts...).
			Height(fittedSelectHeight(len(opts))).
			Value(&picked)
		formFrame = formFirstFrame(msField)
		if !strings.HasSuffix(formFrame, "\n") {
			formFrame += "\n"
		}
	}

	err := ui.RunCardStepsInto(steps, func() string {
		if formGate() && !ui.IsRaw() {
			buildSelectionForm()
			header := ui.NewCard(ui.CardInput, "pull requests").Tight().Render()
			headerLines = strings.Count(header, "\n")
			return header + formFrame
		}
		applyDefaults()
		return buildReviewTargetsCard(resolved).Render()
	})
	if err != nil {
		return err
	}

	if !formGate() {
		// No form — the successor card already recorded the outcome in
		// interactive mode, and finalView()'s side effects (applyDefaults)
		// already ran in raw/plain mode. Re-call to be safe: idempotent.
		applyDefaults()
		// Nothing takes the region over after all, so the targets card
		// the final frame painted is the timeline's tail. Tell the
		// timeline, or its rows keep a blank gutter while the spine
		// resumes on the cards printed below.
		ui.RecordOpenCard(buildReviewTargetsCard(resolved))
		return nil
	}

	if msField == nil {
		// Raw rendering: RunCardStepsInto runs its final-frame closure
		// in raw mode for side effects only (no ANSI painted), so the
		// form hasn't been built. Build it now and run it standalone,
		// skipping the cursor takeover below, which assumes a painted
		// frame to repaint over. Same shape as prerelease's target picker.
		buildSelectionForm()
		if err := runForm(msField); err != nil {
			return err
		}
	} else {
		// Seamless takeover: the form's first frame is already on screen —
		// move the cursor to its origin and let huh repaint the same bytes
		// in place. ClearSpacer: the header was painted by the spinner
		// program's final frame (not via Print), so suppress the spacer the
		// way Tight-on-Print would.
		fmt.Printf("\x1b[%dF", strings.Count(formFrame, "\n"))
		ui.ClearSpacer()
		if err := runForm(msField); err != nil {
			ui.RequestSpacer()
			return err
		}
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

	// huh cleared its frame on submit, leaving the cursor at the form's
	// origin (the line below the header). Erase the header above and
	// drop the record card in its place. ClearSpacer first: runForm's
	// prologue armed the spacer flag, but the gather program's original
	// spacer line is still on screen above the header — printing another
	// would double-space.
	if headerLines > 0 {
		fmt.Printf("\x1b[%dF\x1b[J", headerLines)
	}
	ui.ClearSpacer()
	buildReviewTargetsCard(resolved).Print()
	return nil
}

// resolveDefaultBranch answers "what does this repo's PR target by
// default?" — the host's default_branch first (authoritative, and the
// only source that's right for a repo whose local origin/HEAD was never
// fetched), then the local clone's origin/HEAD, then "" so the caller
// falls back to defaultBaseBranch. Deliberately error-free: a base we
// couldn't detect isn't a review failure, it's a value the global
// --base / config override and the per-repo customization pass exist to
// correct.
func resolveDefaultBranch(ctx context.Context, host code.Host, g vcs.VCS, rc *repoContext) string {
	if host != nil {
		if b, err := host.GetDefaultBranch(ctx, rc.owner, rc.repoName); err == nil && b != "" {
			return b
		}
	}
	if g != nil {
		if b, err := g.GetDefaultBranch(ctx, rc.repo.Path); err == nil && b != "" {
			return b
		}
	}
	return ""
}

// buildReviewTargetsCard composes the static "Pull Requests" card: one
// row per PR-able repo. Each row mirrors its selection-form counterpart —
// the repo plus its PR status (#N (state), or nothing when none) — with
// the glyph carrying whether the repo is acted on (✓) or skipped (○). The
// plan that follows is the single source for create vs. update vs. no-op.
// The card glyph aggregates worst-first (fail > skipped > success) the way
// group parents aggregate children.
func buildReviewTargetsCard(resolved []repoContext) *ui.Card {
	idx := make([]int, len(resolved))
	for i := range resolved {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return resolved[idx[a]].repo.Name < resolved[idx[b]].repo.Name
	})

	var nOK, nSkip, nFail int
	for i := range resolved {
		switch {
		case resolved[i].prErr != nil:
			nFail++
		case resolved[i].include:
			nOK++
		default:
			nSkip++
		}
	}
	state := ui.CardSuccess
	switch {
	case nFail > 0:
		state = ui.CardFailed
	case nSkip > 0 && nOK == 0:
		state = ui.CardSkipped
	}

	card := ui.NewCard(state, "pull requests")
	for _, i := range idx {
		glyph, content := reviewTargetRow(&resolved[i], resolved[i].include)
		card.Item(glyph, content)
	}
	return card
}

// reviewTargetRow renders one repo's status row — the shared shape
// between the gather's progressively-filling card and the record card,
// so the two can't drift. on is the effective inclusion (the
// pre-selection during gather, the user's choice afterward). Included
// rows keep the repo in primary color; skipped rows recede entirely
// (repo segment drops to muted too). A blank note (repo with no PR)
// renders the repo name alone, no trailing " · ".
func reviewTargetRow(rc *repoContext, on bool) (glyph, content string) {
	primary := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

	sel := func(name, note string) string {
		if note == "" {
			return primary.Render(name)
		}
		return primary.Render(name) + muted.Render(" · "+note)
	}
	unsel := func(name, note string) string {
		if note == "" {
			return muted.Render(name)
		}
		return muted.Render(name + " · " + note)
	}

	name := rc.repo.Name
	switch {
	case rc.prErr != nil:
		return lipgloss.NewStyle().Foreground(ui.Palette.Error).Render(ui.Palette.Cross),
			sel(name, rc.prErr.Error())
	case on:
		return lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check),
			sel(name, rc.statusNote())
	default:
		return muted.Render(ui.Palette.Inactive), unsel(name, rc.statusNote())
	}
}

// prState carries per-repo PR discoveries from a pr-action's Assess
// to its Apply: the existing PR (zero value when none) and the
// per-axis deltas to apply.
//
// For a create, add* hold the full requested sets. For an in-place
// update they hold only what's missing from the PR, and contentChanged
// (with title/body/base) drives an EditPR overwrite. There is no
// removal axis — see planSync for why reconciliation is additive.
type prState struct {
	pr code.PullRequest

	addRevs  []string
	addTeams []string
	addAsns  []string

	contentChanged bool
	title          string
	body           string
	base           string
}

// planSync computes the deltas needed to reconcile an existing PR to
// the requested state: reviewers/teams/assignees to ADD, plus whether
// title/body/base differ and must be overwritten.
//
// Reconciliation is deliberately additive-only: config and flags say
// who must be on the PR, not who may be. A reviewer or assignee a
// teammate added out-of-band is collaboration signal, not config
// drift — stripping them (which reconcile-to-config did) silently
// undid a human's decision on every re-run.
//
// A wanted reviewer counts as satisfied when they're pending
// (RequestedReviewers) OR have already submitted a review (ReviewedBy)
// — GitHub drops submitters from the pending list, so diffing against
// pending alone re-requested completed reviewers on every run,
// resetting their review state.
func planSync(existing code.PullRequest, reviewers, teamReviewers, assignees []string, title, body, base string) *prState {
	satisfiedRevs := append(append([]string{}, existing.RequestedReviewers...), existing.ReviewedBy...)
	return &prState{
		pr:       existing,
		addRevs:  diffCaseInsensitive(reviewers, satisfiedRevs),
		addTeams: diffCaseInsensitive(teamReviewers, existing.RequestedTeams),
		addAsns:  diffCaseInsensitive(assignees, existing.Assignees),
		title:    title,
		body:     body,
		base:     base,
		contentChanged: existing.Title != title ||
			existing.Body != body ||
			existing.BaseRef != base,
	}
}

// hasChanges reports whether a sync has any delta to apply.
func (s *prState) hasChanges() bool {
	return s.contentChanged ||
		len(s.addRevs)+len(s.addTeams)+len(s.addAsns) > 0
}

// syncSummary renders a compact delta summary for an in-place PR update,
// e.g. "content +2/-1 rev +1 asn".
func syncSummary(s *prState) string {
	var parts []string
	if s.contentChanged {
		parts = append(parts, "content")
	}
	if a := len(s.addRevs) + len(s.addTeams); a > 0 {
		parts = append(parts, fmt.Sprintf("+%d rev", a))
	}
	if a := len(s.addAsns); a > 0 {
		parts = append(parts, fmt.Sprintf("+%d asn", a))
	}
	return strings.Join(parts, " ")
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

			detail, _ := emitLifecyclePreamble(ctx, issue)

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
				identity, err := repoIdentity(ctx, host, rr.repo.Path)
				if err != nil {
					ui.Fail(fmt.Sprintf("%s: %v", rr.repo.Name, err))
					continue
				}
				resolved = append(resolved, repoContext{
					repo: rr.repo, branch: rr.branch,
					owner: identity.Owner, repoName: identity.Name,
					cfg: loadRepoConfig(rr.repo),
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
			if err := selectReviewTargets(ctx, cmd, host, gitClient, resolved); err != nil {
				return err // ErrCancelled (form abort) propagates as a clean abort
			}

			// --- Resolve PR metadata from flags, config, and interactive prompts ---
			//
			// Two layers. This pass resolves what applies to EVERY repo;
			// customizeRepoMetadata below is the opt-in pass where a repo
			// diverges. Base is the exception that motivates the split: it
			// defaults per repo (each repo's own default branch) and only
			// goes workspace-wide when --base or config says so, because a
			// base that doesn't exist in repo N fails N's CreatePR.

			// globalBase is "" when nothing overrides the per-repo
			// resolution. Config no longer feeds it: pull_request.base
			// is repo-scoped now, so it is read inside baseBranch()
			// per repo rather than hoisted to a workspace-wide value
			// here. Only --base and the prompt still speak for every
			// repo, which is what they were always for.
			globalBase, _ := cmd.Flags().GetString("base")
			if promptForValues(cmd) && !cmd.Flags().Changed("base") {
				// Open-ended text input whose placeholder is whatever
				// submitting nothing yields — the override when one is set,
				// the shared default branch when the repos agree, otherwise
				// a description of the per-repo rule. A typed answer becomes
				// the workspace-wide override.
				entry, err := typeaheadInputOptional("Base Branch", basePlaceholder(globalBase, resolved))
				if err != nil {
					return err
				}
				if entry != "" {
					globalBase = entry
				}
			}

			// baseFor resolves one repo's base: global override first, else
			// the repo's own default branch.
			baseFor := func(rc *repoContext) string {
				if globalBase != "" {
					return globalBase
				}
				return rc.baseBranch()
			}

			// Title and body render from a per-repo template (Branch and
			// BaseBranch differ), so they stay templated unless the user
			// pins a literal via --title/--body or by editing the prompt.
			// The prompts have to preview one concrete rendering: use the
			// first repo this run will actually write to, so the previewed
			// base isn't one no created PR will use. Nil when nothing is
			// writable — templateData renders the issue-only fields in
			// that case, which is all a run with no PR to open can show.
			var repRepo *repoContext
			if writable := writableRepos(resolved); len(writable) > 0 {
				repRepo = &resolved[writable[0]]
			}
			templateData := func(rc *repoContext) prTemplateData {
				d := prTemplateData{
					IssueKey:   issue,
					IssueTitle: detail.Title,
					IssueType:  detail.Type,
					IssueURL:   detail.URL,
				}
				if rc != nil {
					d.Branch = rc.branch
					d.BaseBranch = baseFor(rc)
				}
				return d
			}

			prTitle, _ := cmd.Flags().GetString("title")
			titlePinned := prTitle != ""
			if promptForValues(cmd) && !cmd.Flags().Changed("title") {
				entry, err := typeaheadInputOptional("Title", buildPRTitle(templateData(repRepo)))
				if err != nil {
					return err
				}
				if entry != "" {
					prTitle, titlePinned = entry, true
				}
			}

			prBody, _ := cmd.Flags().GetString("body")
			bodyPinned := prBody != ""
			if promptForValues(cmd) && !cmd.Flags().Changed("body") {
				rendered := buildPRBody(templateData(repRepo))
				val, err := typeaheadText("Body", rendered)
				if err != nil {
					return err
				}
				// Only an EDITED body pins a literal workspace-wide;
				// submitting the preview unchanged leaves each repo
				// rendering its own. Compare ignoring trailing newlines —
				// the textarea round-trip is allowed to normalize those,
				// and reading that as an edit would freeze the preview
				// repo's body onto every repo.
				if strings.TrimRight(val, "\n") != strings.TrimRight(rendered, "\n") {
					prBody, bodyPinned = val, true
				}
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
			//
			// Config is repo-scoped now, so these resolve to a FUNCTION
			// of the repository rather than to one list. That is the
			// whole point of the per-repo layer: the old single list was
			// read once for the entire fan-out and applied to every PR
			// in it, so a team that owns one repository was requested on
			// all of them.
			//
			// Flags and the prompt still speak workspace-wide, and the
			// two do it differently, matching how they always have:
			// --reviewer ADDS to whatever each repository resolved,
			// while an answered prompt PINS one list for every
			// repository. The prompt is one question over one candidate
			// list, so the answer can only mean the latter; the
			// customization pass below is where a repo diverges from it.
			flagReviewers, _ := cmd.Flags().GetStringSlice("reviewer")
			flagTeams, _ := cmd.Flags().GetStringSlice("team-reviewer")
			flagAssignees, _ := cmd.Flags().GetStringSlice("assignee")

			// Resolve self-assign before the interactive prompt so the
			// current user appears pre-selected in the list.
			selfAssign := !viper.IsSet("code_host.pr.self_assign") || viper.GetBool("code_host.pr.self_assign")
			if cmd.Flags().Changed("self-assign") {
				selfAssign, _ = cmd.Flags().GetBool("self-assign")
			}

			// Non-nil once a prompt has pinned that list workspace-wide.
			var pinnedRevs, pinnedTeams, pinnedAsns []string

			// reviewersFor resolves one repo's reviewers. GitHub forbids
			// requesting a review from the PR author (422), and the
			// typeahead already filters self out of its candidates —
			// dropping the author here too is what makes the config,
			// --reviewer and -y paths agree with it.
			reviewersFor := func(rc *repoContext) []string {
				out := pinnedRevs
				if out == nil {
					out = append(slices.Clone(rc.prConfig().StringSlice(prReviewersKey)), flagReviewers...)
				}
				return withoutUser(out, selfUser)
			}
			teamsFor := func(rc *repoContext) []string {
				if pinnedTeams != nil {
					return slices.Clone(pinnedTeams)
				}
				return append(slices.Clone(rc.prConfig().StringSlice(prTeamReviewersKey)), flagTeams...)
			}
			assigneesFor := func(rc *repoContext) []string {
				if pinnedAsns != nil {
					return slices.Clone(pinnedAsns)
				}
				out := append(slices.Clone(rc.prConfig().StringSlice(prAssigneesKey)), flagAssignees...)
				if selfAssign && selfUser != "" && !slices.ContainsFunc(out, func(a string) bool {
					return strings.EqualFold(a, selfUser)
				}) {
					out = append(out, selfUser)
				}
				return out
			}

			// The prompts preview ONE repo's resolution, the same
			// representative repo the title and body previews use, so
			// the pre-selected values belong to a repo this run will
			// actually write to.
			if promptForValues(cmd) && !cmd.Flags().Changed("reviewer") && host != nil {
				selected, err := typeaheadMultiSelect("Reviewers", reviewersFor(repRepo), func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				}, excludeUser(selfUser))
				if err != nil {
					return err
				}
				pinnedRevs = nonNilSlice(selected)
			}

			if promptForValues(cmd) && !cmd.Flags().Changed("team-reviewer") && host != nil {
				selected, err := typeaheadMultiSelect("Team Reviewers", teamsFor(repRepo), func() ([]string, error) {
					return host.ListTeams(ctx, apiOwner)
				})
				if err != nil {
					return err
				}
				pinnedTeams = nonNilSlice(selected)
			}

			if promptForValues(cmd) && !cmd.Flags().Changed("assignee") && host != nil {
				selected, err := typeaheadMultiSelect("Assignees", assigneesFor(repRepo), func() ([]string, error) {
					return host.ListCollaborators(ctx, apiOwner, apiRepo)
				}, promoteUser(selfUser))
				if err != nil {
					return err
				}
				pinnedAsns = nonNilSlice(selected)
			}

			// --- Seed per-repo metadata, then offer the override pass ---
			//
			// Every repo starts from the shared resolution above; only
			// base (and the templated title/body that embed it) is
			// already per repo at this point.
			for i := range resolved {
				rc := &resolved[i]
				data := templateData(rc)
				rc.meta = prMetadata{
					base:      data.BaseBranch,
					title:     prTitle,
					body:      prBody,
					reviewers: reviewersFor(rc),
					teams:     teamsFor(rc),
					assignees: assigneesFor(rc),
				}
				if !titlePinned {
					rc.meta.title = buildPRTitle(data)
				}
				if !bodyPinned {
					rc.meta.body = buildPRBody(data)
				}
			}

			if err := customizeRepoMetadata(ctx, cmd, host, resolved, selfUser); err != nil {
				return err // ErrCancelled (form abort) propagates as a clean abort
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
					// creation. meta is THIS repo's PR content — shared
					// values unless the customization pass changed them.
					existing := rc.pr
					prErr := rc.prErr
					include := rc.include
					meta := rc.meta

					// An open/draft PR is a candidate for in-place update;
					// anything else (none, closed, merged) is a creation
					// candidate. Both are gated by the user's selection.
					// Taken from the shared predicate rather than recomputed
					// below: selectReviewTargets already classified this repo
					// with it, and a second inline copy is free to drift from
					// the one that decided which repos were even offered.
					active := rc.activePR()

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

							// Every open/draft PR in the workspace is recorded
							// for the notification, whether or not we mutate
							// it. When we will update it, the record carries
							// the desired title/body/base so the notification
							// reflects the post-update state.
							if active {
								rec := existing
								if include {
									rec.Title = meta.title
									rec.Body = meta.body
									rec.BaseRef = meta.base
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

								// Checked for update — add whichever
								// reviewers, teams, and assignees are missing
								// and overwrite title/body/base when they
								// differ.
								*state = *planSync(existing, meta.reviewers, meta.teams, meta.assignees, meta.title, meta.body, meta.base)
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
								// Plan-detail grammar: persisting state first
								// (the PR that stays as-is), why parenthesized.
								if existing.Number > 0 {
									return ActionCompleted, fmt.Sprintf("#%d %s (not selected)", existing.Number, existing.State), nil
								}
								return ActionCompleted, "(not selected)", nil
							}

							// Selected for creation — Apply opens a new PR
							// and applies all requested reviewers/teams/
							// assignees fresh.
							state.addRevs = meta.reviewers
							state.addTeams = meta.teams
							state.addAsns = meta.assignees
							detail := fmt.Sprintf("%s → %s", branch, meta.base)
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
									Base:       meta.base,
									Title:      meta.title,
									Body:       meta.body,
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
							if len(state.addAsns) > 0 {
								if err := host.AddAssignees(ctx, owner, repoName, pr.Number, state.addAsns); err != nil {
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
			reviewChannel := viper.GetString("notification.channels.review")
			notifier, notifierErr := newNotifier()
			if notifierErr == nil {
				defer notifier.Close()
			}

			// Resolve the author's avatar for card icons — from the login
			// the reviewer-exclusion pass already fetched (best-effort; an
			// empty selfUser just means no avatar).
			authorAvatar := avatarURL(host, selfUser)

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
						BranchURL: branchURL(host, r.owner, r.repoName, r.branch),
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
						// A failed lookup must not read as "no thread"
						// — Apply would post a duplicate instead of
						// updating the existing notification. ✗-row it.
						ref, err := notifier.FindThread(ctx, reviewChannel, issue)
						if err != nil {
							return 0, "", fmt.Errorf("finding notification thread: %w", err)
						}
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
							IssueKey:         issue,
							IssueTitle:       detail.Title,
							IssueType:        detail.Type,
							IssueURL:         detail.URL,
							IssueDescription: detail.Description,
							IssueIconURL:     detail.TypeIconURL,
							IconURL:          authorAvatar,
							Items:            notifyItems(),
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
								IssueKey:         issue,
								IssueTitle:       detail.Title,
								IssueType:        detail.Type,
								IssueURL:         detail.URL,
								IssueDescription: detail.Description,
								IssueIconURL:     detail.TypeIconURL,
								IconURL:          authorAvatar,
								Items:            items,
							}),
						})
						return err
					},
				})
			} else if !draft && notifierErr == nil {
				// Notification is configured (the notifier built), but this
				// command has no channel to post to — a partial config, not an
				// opt-out. Surface it (naming the key) rather than dropping the
				// announcement silently. Drafts intentionally suppress
				// notifications, so they stay quiet; so does an unconfigured
				// provider.
				ui.Skip("notification: set notification.channels.review to announce the review")
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
	cmd.Flags().String("base", "", "target branch for every repo (default: each repo's pull_request.base, else its own default branch)")
	cmd.Flags().String("title", "", "override PR title")
	cmd.Flags().String("body", "", "override PR body")
	cmd.Flags().StringSlice("reviewer", nil, "request review from user (repeatable)")
	cmd.Flags().StringSlice("team-reviewer", nil, "request review from team (repeatable)")
	cmd.Flags().StringSlice("assignee", nil, "assign PR to user (repeatable)")
	cmd.Flags().Bool("self-assign", false, "assign PR to yourself")
	return cmd
}
