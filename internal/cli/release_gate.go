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
// deployedKnown, else undeterminable). Pure.
//
//	T == ""                → block  (no release contains this work)
//	!deployedKnown         → go     (can't verify what's live → deploy, permissive)
//	D == ""                → go     (first deploy)
//	compareSemverTag(D,T)≥0 → skip   (already live at D / D newer → would roll back)
//	else                   → go     (D → T)
//
// Detail strings follow the plan-detail grammar — the state/diff first,
// any why as a trailing parenthetical — except the block reason, which
// has no diff and stays plain (the plan wraps it, cards show it as-is).
func classifyServiceDeploy(containingTag, deployedTag string, deployedKnown bool) (deployState, string) {
	if containingTag == "" {
		return deployBlock, "no release contains this work — run prerelease"
	}
	if !deployedKnown {
		return deployGo, "→ " + containingTag + " (deployed state unknown)"
	}
	if deployedTag == "" {
		return deployGo, "→ " + containingTag + " (first deploy)"
	}
	if compareSemverTag(deployedTag, containingTag) >= 0 {
		return deploySkip, deployedTag + " (already live)"
	}
	return deployGo, deployedTag + " → " + containingTag
}

// releaseServiceTarget is one deploy target resolved with its work tag
// (T), deployed tag (D), and classification.
type releaseServiceTarget struct {
	target      DeployTarget
	workTag     string // T (repo-level)
	deployedTag string // D (per-service)
	latestTag   string // repo's latest release, for the "newer exists" note
	state       deployState
	reason      string
	infoNote    string
	err         error // identity/resolution failure → ✗ row (siblings unaffected)

	// include is the user's selection (or the default: every deployGo
	// target). Deselected targets render as "not selected" no-ops in
	// the record card and plan, mirroring prerelease's deselection.
	include bool
}

// deployTargetResolver resolves targets one at a time so the gather
// spinner can narrate per-target progress. T (and the repo's latest
// tag) is computed once per repo and cached across targets; D is
// per-target. Best-effort: a failure records an err on that target's
// state rather than aborting the others.
type deployTargetResolver struct {
	g          vcs.VCS
	host       code.Host
	pathByName map[string]string
	cache      map[string]deployRepoInfo
}

// deployRepoInfo is the per-repo half of target resolution.
type deployRepoInfo struct {
	workTag, latestTag string
	err                error
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

// repoInfo returns the repo-level resolution (T + latest tag) for a
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
	if branch, berr := r.g.GetCurrentBranch(ctx, path); berr == nil && branch != "" {
		if pr, perr := r.host.GetPRForBranch(ctx, dt.Owner, dt.Repo, branch); perr == nil &&
			pr.State == "merged" && pr.MergeCommitSHA != "" {
			workSHA = pr.MergeCommitSHA
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
	if lt, lerr := r.host.GetLatestTag(ctx, dt.Owner, dt.Repo); lerr == nil {
		ri.latestTag = lt
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
	st.latestTag = ri.latestTag

	// D: the latest successful deployment of this environment, mapped
	// to a release tag. Prefer the ref when it's already a tag; else
	// resolve the deployed SHA to its lowest containing release tag.
	deployedKnown := true
	dep, derr := r.host.GetLatestDeployment(ctx, dt.Owner, dt.Repo, dt.Environment)
	switch {
	case errors.Is(derr, code.ErrNotFound):
		st.deployedTag = "" // known: never deployed
	case derr != nil:
		deployedKnown = false // couldn't determine
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

	st.state, st.reason = classifyServiceDeploy(st.workTag, st.deployedTag, deployedKnown)

	if st.workTag != "" && st.latestTag != "" && compareSemverTag(st.latestTag, st.workTag) > 0 {
		st.infoNote = "newer release exists: " + st.latestTag
	}
	return st
}

// selectServiceDeploys runs release's Observe → Select → record arc,
// mirroring prerelease's selectReleaseTargets: gather each target's
// deployed state + classification under a spinner, then (interactive,
// not -y) offer a multi-select over the deployable targets — every
// deployGo target pre-checked, skip/block/error rows shown inert for
// visibility — and finally record the outcome in the Deploy card. The
// plan interprets the selection (deselected → "= not selected").
// Non-interactive runs take the defaults (every deployGo included).
func selectServiceDeploys(ctx context.Context, cmd *cobra.Command, host code.Host, workspace string, targets []DeployTarget) ([]releaseServiceTarget, error) {
	g := git.New()
	var states []releaseServiceTarget

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
	applyDefaults := func() {
		for i := range states {
			states[i].include = states[i].state == deployGo
		}
	}

	resolver, err := newDeployTargetResolver(ctx, g, host, workspace)
	if err != nil {
		return nil, err
	}

	// One spinner step per target with a stable-shape progress line —
	// `<reason>: <target> (<n>/<total>)` — so fast steps read as
	// progress (only the item and counter change) and slow steps show
	// exactly what's being waited on.
	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	steps := make([]ui.CardStep, 0, len(targets))
	for i, dt := range targets {
		body := statusMuted.Render("Checking deployed versions: ") + ui.Keyword(dt.Label)
		if len(targets) > 1 {
			body += statusMuted.Render(fmt.Sprintf(" (%d/%d)", i+1, len(targets)))
		}
		steps = append(steps, ui.CardStep{
			Card: ui.NewCard(ui.CardRunning, "deploy").Raw(body),
			Run: func() error {
				states = append(states, resolver.resolve(ctx, dt))
				return nil
			},
		})
	}

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
		applyDefaults()
		return states, nil
	}

	// Selection form: one row per target. Deployable rows are
	// toggleable and pre-checked; skip/block/error rows appear inert
	// (visible with their reason, toggles no-op'd at parse — huh has
	// no disabled options) so the user sees every target accounted
	// for without leaving the picker.
	opts := make([]huh.Option[string], 0, len(states))
	for i := range states {
		st := &states[i]
		// Bold the repo segment via raw SGR toggles (lipgloss's
		// closing reset would wipe huh's selection styling).
		label := "\x1b[1m" + st.target.RepoName + "\x1b[22m · " + st.target.Service
		switch {
		case st.err != nil:
			label += " · " + st.err.Error()
		default:
			label += " · " + st.reason
		}
		if st.infoNote != "" {
			label += " (" + st.infoNote + ")"
		}
		opts = append(opts, huh.NewOption(label, strconv.Itoa(i)).Selected(st.state == deployGo))
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

	// Erase the form header and drop the record card in its place.
	rewind()
	buildDeployTargetsCard(states).Print()
	return states, nil
}

// buildDeployTargetsCard renders the "deploy" record card: one row per
// service target with a glyph (✓ deploying / ○ deselected, skipped, or
// blocked / ✗ error) and the classification reason, plus a muted
// continuation row for any "newer release exists" note. Mirrors
// buildReleaseTargetsCard: rows are status, the plan owns the change.
func buildDeployTargetsCard(states []releaseServiceTarget) *ui.Card {
	primary := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphOff := muted.Render("○")
	glyphFail := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render("✗")

	type row struct{ label, glyph, content string }
	var rows []row
	var nOK, nSkip, nFail int

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

	info := make(map[string]string, len(states))
	for _, st := range states {
		if st.infoNote != "" {
			info[st.target.Label] = st.infoNote
		}
		switch {
		case st.err != nil:
			nFail++
			rows = append(rows, row{st.target.Label, glyphFail, on(st.target.Label, st.err.Error())})
		case st.state == deployGo && st.include:
			nOK++
			rows = append(rows, row{st.target.Label, glyphOK, on(st.target.Label, st.reason)})
		case st.state == deployGo:
			// Deployable but deselected in the form. Pure status — the
			// currently-deployed version (the service stays there); the
			// plan carries the "not selected" reason. Mirrors
			// prerelease's card/plan split for deselected repos.
			nSkip++
			note := st.deployedTag
			if note == "" {
				note = "(none)"
			}
			rows = append(rows, row{st.target.Label, glyphOff, off(st.target.Label, note)})
		default: // deploySkip / deployBlock
			nSkip++
			rows = append(rows, row{st.target.Label, glyphOff, off(st.target.Label, st.reason)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })

	state := ui.CardSuccess
	switch {
	case nFail > 0:
		state = ui.CardFailed
	case nSkip > 0 && nOK == 0:
		state = ui.CardSkipped
	}

	card := ui.NewCard(state, "deploy")
	infoGlyph := muted.Render("+")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
		if n, ok := info[r.label]; ok {
			card.Item(infoGlyph, muted.Render(n))
		}
	}
	return card
}
