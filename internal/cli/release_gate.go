package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
)

// deployState is one service's production-deploy decision.
type deployState int

const (
	deployUnknown deployState = iota
	deployGo                  // dispatch: deploy the containing tag T
	deploySkip                // already at/past T — no-op
	deployBlock               // no release contains the work — cannot deploy
)

// classifyServiceDeploy decides one service's production action from the
// repo-level tag containing the work (T; "" = no release contains it)
// and the service's currently-deployed tag (D; "" = never deployed when
// deployedKnown, else undeterminable). explicit marks a --tag override
// — an operator naming exactly what to ship. Pure.
//
//	workTag == ""            → block  (no release contains this work)
//	!deployedKnown           → go     (can't verify what's live → deploy, permissive)
//	D == ""                  → go     (first deploy)
//	compareSemverTag(D,T)≥0  → skip   (already live at D / D newer → would roll back)
//	  …unless explicit, D≠T  → go     (a --tag naming an older release IS a rollback request)
//	else                     → go     (D → T)
//
// workTag (the release containing the workspace's work) is the GATE;
// deployTag (the workspace release by default, --tag to override) is
// what ships. The
// two are split so "is the work released?" and "which release do we
// deploy?" stay independent questions.
//
// Detail strings follow the plan-detail grammar — the state/diff first,
// any why as a trailing parenthetical — except the block reason, which
// has no diff and stays plain (the plan wraps it, cards show it as-is).
func classifyServiceDeploy(workTag, deployTag, deployedTag string, deployedKnown, explicit bool) (deployState, string) {
	if workTag == "" {
		return deployBlock, "no release contains this work — run prerelease"
	}
	if !deployedKnown {
		return deployGo, "→ " + deployTag + " (deployed state unknown)"
	}
	if deployedTag == "" {
		return deployGo, "→ " + deployTag + " (first deploy)"
	}
	if compareSemverTag(deployedTag, deployTag) >= 0 {
		if explicit && deployedTag != deployTag {
			return deployGo, deployedTag + " → " + deployTag + " (rollback, --tag)"
		}
		return deploySkip, deployedTag + " (already live)"
	}
	return deployGo, deployedTag + " → " + deployTag
}

// repoNoopDetail composes the plan detail for a repo shipping nothing —
// the one "=" row that stands in for all of the repo's unselected
// service targets. Plan-detail grammar: persisting state (the release
// that would have shipped) first, why parenthesized. Precedence: a
// gate block is the load-bearing why; else any unselected-deployable
// means "not selected"; else everything was already live.
func repoNoopDetail(states []releaseServiceTarget, repo string) string {
	var workTag string
	anyDeployable, anyBlocked := false, false
	blockReason := ""
	for i := range states {
		st := &states[i]
		if st.target.RepoName != repo {
			continue
		}
		if st.workTag != "" {
			workTag = st.workTag
		}
		switch st.state {
		case deployGo:
			anyDeployable = true
		case deployBlock:
			anyBlocked = true
			if blockReason == "" {
				blockReason = st.reason
			}
		}
	}
	why := "already live"
	switch {
	case anyBlocked && !anyDeployable:
		why = blockReason
	case anyDeployable:
		why = "not selected"
	}
	if workTag != "" {
		return workTag + " (" + why + ")"
	}
	return "(" + why + ")"
}

// advanceReleaseStatus decides whether a release run may move the
// issue to done: something actually deployed, or nothing needed to
// (everything already live, none blocked, none erred). Pure.
//
// Deselecting every deploy or having blocked-only work holds the
// status — the issue isn't in production yet. observed=false (CI/CD
// or code-host setup failed, so no target was ever classified) also
// holds it: "couldn't look" must not read as "nothing to do" and mark
// work done that never shipped. Zero *configured* targets still
// advances (observed with an empty states slice) — a project with no
// deploy targets uses release purely as the status transition.
func advanceReleaseStatus(observed bool, states []releaseServiceTarget) bool {
	if !observed {
		return false
	}
	anyDeploy, anyDeployable, anyBlock := false, false, false
	for _, st := range states {
		if st.err != nil {
			// An unresolved target is an unknown, not a no-op: some
			// service's production state couldn't even be classified,
			// so the issue can't be called done this run.
			return false
		}
		switch st.state {
		case deployGo:
			anyDeployable = true
			if st.include {
				anyDeploy = true
			}
		case deployBlock:
			anyBlock = true
		}
	}
	return anyDeploy || (!anyDeployable && !anyBlock)
}

// chooseDeployTag picks the tag a service deploys: an explicit --tag
// override wins; else the workspace's own release (the one prerelease
// cut — whose contents, including swept-in extras, are what the team
// confirms before shipping). Deliberately NOT the repo's latest
// release: tags cut after ours are other people's deploys to
// coordinate, not ours to absorb. Pure.
func chooseDeployTag(workTag, override string) string {
	if override != "" {
		return override
	}
	return workTag
}

// releaseServiceTarget is one deploy target resolved with its work tag
// (T), deployed tag (D), and classification.
type releaseServiceTarget struct {
	target      DeployTarget
	workTag     string // release containing the work (repo-level; the gate)
	deployTag   string // tag to ship: the workspace release, or --tag override
	deployedTag string // D (per-service)
	state       deployState
	reason      string
	err         error // identity/resolution failure → ✗ row (siblings unaffected)

	// include is the user's selection (default: nothing, unless the
	// targets were --service-named — see selectServiceDeploys).
	// Unselected targets render as "not selected" no-ops in the plan,
	// with the record card showing their deployed tag.
	include bool
}

// deployTargetResolver resolves targets one at a time so the gather
// spinner can narrate per-target progress. T (repo-level)
// is computed once per repo and cached across targets; D is
// per-target. Best-effort: a failure records an err on that target's
// state rather than aborting the others.
type deployTargetResolver struct {
	g           vcs.VCS
	host        code.Host
	pathByName  map[string]string
	cache       map[string]deployRepoInfo
	tagOverride string // --tag: deploy this tag instead of the workspace's release
}

// deployRepoInfo is the per-repo half of target resolution.
type deployRepoInfo struct {
	workTag string
	err     error
}

// newDeployTargetResolver builds the resolver's repo lookup for the
// active workspace.
func newDeployTargetResolver(ctx context.Context, g vcs.VCS, host code.Host, workspace string) (*deployTargetResolver, error) {
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	pathByName := make(map[string]string, len(repos))
	for _, r := range repos {
		pathByName[r.Name] = r.Path
	}
	return &deployTargetResolver{
		g:          g,
		host:       host,
		pathByName: pathByName,
		cache:      make(map[string]deployRepoInfo),
	}, nil
}

// repoInfo returns the repo-level resolution (T) for a
// target's repo, computing it on first use.
func (r *deployTargetResolver) repoInfo(ctx context.Context, dt DeployTarget) deployRepoInfo {
	if ri, ok := r.cache[dt.RepoName]; ok {
		return ri
	}
	ri := deployRepoInfo{}
	path := r.pathByName[dt.RepoName]
	if path == "" {
		ri.err = fmt.Errorf("%s: not an active workspace repo", dt.RepoName)
		r.cache[dt.RepoName] = ri
		return ri
	}
	// Work SHA: the workspace PR's merge commit when merged (on the
	// default branch), else local HEAD. Mirrors prerelease's probe.
	workSHA := ""
	var prLookupErr error
	if branch, berr := r.g.GetCurrentBranch(ctx, path); berr == nil && branch != "" {
		pr, perr := r.host.GetPRForBranch(ctx, dt.Owner, dt.Repo, branch)
		if perr == nil {
			if pr.State == "merged" && pr.MergeCommitSHA != "" {
				workSHA = pr.MergeCommitSHA
			}
		} else {
			prLookupErr = perr
		}
	}
	if workSHA == "" {
		if sha, herr := r.g.HeadSHA(ctx, path); herr == nil {
			workSHA = sha
		}
	}
	_ = r.g.FetchTags(ctx, path, "origin")
	if workSHA != "" {
		if tags, terr := r.g.TagsContaining(ctx, path, workSHA); terr == nil {
			ri.workTag = lowestContainingReleaseTag(tags)
		}
	}
	if ri.workTag == "" && prLookupErr != nil {
		// Local HEAD found no containing tag AND the PR lookup failed:
		// for a squash-merged branch, only the PR's merge commit maps
		// to the containing release, so "no release contains this
		// work" can't be distinguished from "couldn't see the PR". An
		// ✗ row (fail closed) beats a deployBlock that misdirects the
		// user to run prerelease again.
		ri.err = fmt.Errorf("resolving work release: %w", prLookupErr)
	}
	r.cache[dt.RepoName] = ri
	return ri
}

// resolve fills one target's deployed state + classification.
func (r *deployTargetResolver) resolve(ctx context.Context, dt DeployTarget) releaseServiceTarget {
	st := releaseServiceTarget{target: dt}
	ri := r.repoInfo(ctx, dt)
	if ri.err != nil {
		st.err = ri.err
		return st
	}
	st.workTag = ri.workTag
	st.deployTag = chooseDeployTag(ri.workTag, r.tagOverride)

	// D: the latest successful deployment of this environment, mapped
	// to a release tag. Prefer the ref when it's already a tag; else
	// resolve the deployed SHA to its lowest containing release tag.
	// An API error is an observation *failure*, not an observation —
	// fail closed as an ✗ row rather than deployGo: a transient 500
	// while production is ahead would otherwise let a -y run dispatch
	// the older tag and roll production back. deployedKnown=false is
	// reserved for the affirmative "deployed, but not from a release
	// tag" case, where deploying over a branch-deploy is plausible.
	deployedKnown := true
	dep, derr := r.host.GetLatestDeployment(ctx, dt.Owner, dt.Repo, dt.Environment)
	switch {
	case errors.Is(derr, code.ErrNotFound):
		st.deployedTag = "" // known: never deployed
	case derr != nil:
		st.err = fmt.Errorf("deployed state: %w", derr)
		return st
	default:
		path := r.pathByName[dt.RepoName]
		if releaseTagPattern.MatchString(dep.Ref) {
			st.deployedTag = dep.Ref
		} else if dep.SHA != "" {
			if tags, terr := r.g.TagsContaining(ctx, path, dep.SHA); terr == nil {
				st.deployedTag = lowestContainingReleaseTag(tags)
			}
		}
		if st.deployedTag == "" {
			// Deployed, but the ref/SHA doesn't map to a release tag
			// (e.g. deployed from a branch). Treat as undeterminable.
			deployedKnown = false
		}
	}

	st.state, st.reason = classifyServiceDeploy(st.workTag, st.deployTag, st.deployedTag, deployedKnown, r.tagOverride != "")
	return st
}

// selectServiceDeploys runs release's Observe → Select → record arc,
// mirroring prerelease's selectReleaseTargets: gather each target's
// deployed state + classification under a spinner, then (interactive,
// not -y) offer a multi-select over the deployable targets —
// skip/block/error rows shown inert for visibility — and finally
// record the outcome in the Deploy card. The plan interprets the
// selection (unselected → "= not selected").
// Selection defaults to nothing: a monorepo deploy usually targets the
// workspace's affected services, and until D..T affected-narrowing
// exists bosun can't know which those are — so services are opted IN,
// not out. Naming services via --service IS the opt-in: those targets
// (the whole filtered set) pre-check in the form and deploy by default
// non-interactively. Without --service, non-interactive runs deploy
// nothing.
func selectServiceDeploys(ctx context.Context, cmd *cobra.Command, host code.Host, workspace string, targets []DeployTarget) ([]releaseServiceTarget, error) {
	g := git.New()
	var states []releaseServiceTarget

	// --service filtered the target list upstream, so its presence
	// means every remaining target was explicitly requested.
	svcFlag, _ := cmd.Flags().GetStringSlice("service")
	explicit := len(svcFlag) > 0

	formGate := func() bool {
		if !isInteractive() || isAutoApprove(cmd) {
			return false
		}
		for i := range states {
			if states[i].state == deployGo {
				return true
			}
		}
		return false
	}
	preselect := func(st *releaseServiceTarget) bool {
		return st.state == deployGo && explicit
	}
	applyDefaults := func() {
		for i := range states {
			states[i].include = preselect(&states[i])
		}
	}

	resolver, err := newDeployTargetResolver(ctx, g, host, workspace)
	if err != nil {
		return nil, err
	}
	resolver.tagOverride, _ = cmd.Flags().GetString("tag")

	// Accumulating gather: each step's card carries the rows resolved
	// so far plus a pending row — the in-flight target with the live
	// spinner in its glyph slot — so the selection list materializes in
	// place and then swaps into the form (or record card) with the same
	// rows. Rows render via deployStateRow, the same renderer the
	// record card uses, for seam-free continuity.
	//
	// Mutating later cards from a step's Run is safe: the runner's
	// worker sends each step's result over a channel before the model
	// advances to the next card, so a card is never rendered until
	// every earlier step — including its appends to that card — has
	// completed (happens-before via the channel).
	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	n := len(targets)
	pendingRow := func(c *ui.Card, i int) {
		c.Item(ui.GlyphSlot, statusMuted.Render(targets[i].Label))
	}

	cards := make([]*ui.Card, n)
	for i := range cards {
		cards[i] = ui.NewCard(ui.CardRunning, "deploy")
	}
	pendingRow(cards[0], 0)

	steps := make([]ui.CardStep, 0, n)
	for i, dt := range targets {
		steps = append(steps, ui.CardStep{
			Card: cards[i],
			Run: func() error {
				st := resolver.resolve(ctx, dt)
				states = append(states, st)
				// Append this target's resolved row to every later
				// card (gather-time inclusion = the classification),
				// then the next card's pending row beneath the rows.
				glyph, content := deployStateRow(&st, preselect(&st))
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

	// Selection form construction, deferred until the gather has
	// resolved every target (the options are its rows). Under the
	// session shell the form embeds beneath the "deploy" input header
	// in the same program — the formFirstFrame takeover that used to
	// bridge the steps program into huh's program is gone.
	//
	// Rows: deployable targets toggleable and pre-checked;
	// skip/block/error rows inert (visible with their reason, toggles
	// no-op'd at parse — huh has no disabled options) so every target
	// is accounted for inside the picker.
	var (
		picked  []string
		msField *huh.MultiSelect[string]
	)
	buildSelectionForm := func() {
		opts := make([]huh.Option[string], 0, len(states))
		for i := range states {
			st := &states[i]
			// Bold the repo segment via raw SGR toggles (lipgloss's
			// closing reset would wipe huh's selection styling).
			label := "\x1b[1m" + st.target.RepoName + "\x1b[22m · " + st.target.Service
			switch {
			case st.err != nil:
				label += " · " + st.err.Error()
			case st.state == deployGo:
				// Just the current version — it informs the selection
				// (who's behind). The deploy target doesn't: it's
				// constant per repo and belongs to approval, so it
				// shows on the plan's D → T rows instead (prerelease's
				// form-shows-no-versions rationale). Matches the
				// gather rows.
				d := st.deployedTag
				if d == "" {
					d = "(none)"
				}
				label += " · " + d
			default:
				// Inert skip/block rows keep their reason — it's what
				// explains why they can't be selected.
				label += " · " + st.reason
			}
			opts = append(opts, huh.NewOption(label, strconv.Itoa(i)).Selected(preselect(st)))
		}
		// Full height whenever it fits, capped to the terminal: a frame
		// taller than the screen breaks the takeover below rather than
		// just overflowing.
		msField = fittedMultiSelect(opts, &picked)
	}

	// Run the gather; its final card is the tail the next phase builds
	// on — the "deploy" input header when the selection form follows
	// (silent in raw mode: the record card would misreport before the
	// form adjusts the selection), or the record card when no form
	// will show.
	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if formGate() {
			return ui.NewCard(ui.CardInput, "deploy").Tight()
		}
		applyDefaults()
		return buildDeployTargetsCard(states)
	})
	if err != nil {
		return nil, err
	}
	if !formGate() {
		applyDefaults() // no-op when the closure already ran
		return states, nil
	}

	buildSelectionForm()
	if err := runForm(msField); err != nil {
		ui.RequestSpacer()
		return nil, err
	}

	for i := range states {
		states[i].include = false
	}
	for _, p := range picked {
		i, perr := strconv.Atoi(p)
		if perr != nil || i < 0 || i >= len(states) {
			continue
		}
		// Inert rows (skip/block/error) can't be selected for deploy —
		// silently no-op any toggle the user made on them.
		if states[i].state != deployGo {
			continue
		}
		states[i].include = true
	}

	// The submitted form has resolved; erase the input header (a tail
	// drop under the session shell) and put the selection-adjusted
	// record card in its place. The drop re-arms the connector the
	// header consumed — no ClearSpacer here, or the record card prints
	// squished against the card above (#102).
	rewind()
	buildDeployTargetsCard(states).Print()
	return states, nil
}

// deployStateRow renders one target's status row — the shared shape
// between the gather's progressively-filling card, the selection form,
// and the final record card, so none of the three can drift. selected
// is the effective inclusion (the classification default during
// gather, the user's choice afterward).
func deployStateRow(st *releaseServiceTarget, selected bool) (glyph, content string) {
	primary := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

	on := func(label, note string) string {
		if note == "" {
			return primary.Render(label)
		}
		return primary.Render(label) + muted.Render(" · "+note)
	}
	off := func(label, note string) string {
		if note == "" {
			return muted.Render(label)
		}
		return muted.Render(label + " · " + note)
	}

	switch {
	case st.err != nil:
		return lipgloss.NewStyle().Foreground(ui.Palette.Error).Render(ui.Palette.Cross),
			on(st.target.Label, st.err.Error())
	case st.state == deployGo && selected:
		return lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check),
			on(st.target.Label, st.reason)
	case st.state == deployGo:
		// Deployable but deselected in the form. Pure status — the
		// currently-deployed version (the service stays there); the
		// plan carries the "not selected" reason. Mirrors
		// prerelease's card/plan split for deselected repos.
		note := st.deployedTag
		if note == "" {
			note = "(none)"
		}
		return muted.Render(ui.Palette.Inactive), off(st.target.Label, note)
	default: // deploySkip / deployBlock
		return muted.Render(ui.Palette.Inactive), off(st.target.Label, st.reason)
	}
}

// buildDeployTargetsCard renders the "deploy" record card: one row per
// service target with a glyph (✓ deploying / ○ deselected, skipped, or
// blocked / ✗ error) and the classification reason. Mirrors
// buildReleaseTargetsCard: rows are status, the plan owns the change.
func buildDeployTargetsCard(states []releaseServiceTarget) *ui.Card {
	idx := make([]int, len(states))
	for i := range states {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return states[idx[a]].target.Label < states[idx[b]].target.Label
	})

	var nOK, nSkip, nFail int
	for i := range states {
		switch {
		case states[i].err != nil:
			nFail++
		case states[i].state == deployGo && states[i].include:
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

	card := ui.NewCard(state, "deploy")
	shown := 0
	for _, i := range idx {
		st := &states[i]
		// Record only what was selected (plus errors — those need
		// eyes): the opt-in model makes unselected the common case,
		// and the plan below accounts for every target anyway
		// (= not selected / = already live / block reasons).
		keep := st.err != nil || (st.state == deployGo && st.include)
		if !keep {
			continue
		}
		glyph, content := deployStateRow(st, st.include)
		card.Item(glyph, content)
		shown++
	}
	if shown == 0 {
		card.Muted("(none selected)")
	}
	return card
}
