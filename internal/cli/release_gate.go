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
func classifyServiceDeploy(containingTag, deployedTag string, deployedKnown bool) (deployState, string) {
	if containingTag == "" {
		return deployBlock, "no release contains this work — run prerelease"
	}
	if !deployedKnown {
		return deployGo, "deployed state unknown — deploying " + containingTag
	}
	if deployedTag == "" {
		return deployGo, "first deploy → " + containingTag
	}
	if compareSemverTag(deployedTag, containingTag) >= 0 {
		return deploySkip, "already live at " + deployedTag
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

// resolveServiceDeployTargets fills deployed state + classification for
// each target. T is resolved once per repo (the workspace PR's merge
// commit when merged, else HEAD → the lowest release tag containing it);
// D per service comes from the latest successful deployment of the
// target's environment, mapped to a release tag. Best-effort: a failure
// on one target records an err row rather than aborting the others.
func resolveServiceDeployTargets(ctx context.Context, g vcs.VCS, host code.Host, workspace string, targets []DeployTarget) ([]releaseServiceTarget, error) {
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	pathByName := make(map[string]string, len(repos))
	for _, r := range repos {
		pathByName[r.Name] = r.Path
	}

	// Per-repo T + latest tag, computed once and cached.
	type repoInfo struct {
		workTag, latestTag string
		err                error
	}
	cache := make(map[string]repoInfo)
	computeRepo := func(dt DeployTarget) repoInfo {
		if ri, ok := cache[dt.RepoName]; ok {
			return ri
		}
		ri := repoInfo{}
		path := pathByName[dt.RepoName]
		if path == "" {
			ri.err = fmt.Errorf("%s: not an active workspace repo", dt.RepoName)
			cache[dt.RepoName] = ri
			return ri
		}
		// Work SHA: the workspace PR's merge commit when merged (on the
		// default branch), else local HEAD. Mirrors prerelease's probe.
		workSHA := ""
		if branch, berr := g.GetCurrentBranch(ctx, path); berr == nil && branch != "" {
			if pr, perr := host.GetPRForBranch(ctx, dt.Owner, dt.Repo, branch); perr == nil &&
				pr.State == "merged" && pr.MergeCommitSHA != "" {
				workSHA = pr.MergeCommitSHA
			}
		}
		if workSHA == "" {
			if sha, herr := g.HeadSHA(ctx, path); herr == nil {
				workSHA = sha
			}
		}
		_ = g.FetchTags(ctx, path, "origin")
		if workSHA != "" {
			if tags, terr := g.TagsContaining(ctx, path, workSHA); terr == nil {
				ri.workTag = lowestContainingReleaseTag(tags)
			}
		}
		if lt, lerr := host.GetLatestTag(ctx, dt.Owner, dt.Repo); lerr == nil {
			ri.latestTag = lt
		}
		cache[dt.RepoName] = ri
		return ri
	}

	out := make([]releaseServiceTarget, 0, len(targets))
	for _, dt := range targets {
		st := releaseServiceTarget{target: dt}
		ri := computeRepo(dt)
		if ri.err != nil {
			st.err = ri.err
			out = append(out, st)
			continue
		}
		st.workTag = ri.workTag
		st.latestTag = ri.latestTag
		st.deployedTag, st.reason, st.state = "", "", deployUnknown

		// D: the latest successful deployment of this environment, mapped
		// to a release tag. Prefer the ref when it's already a tag; else
		// resolve the deployed SHA to its lowest containing release tag.
		deployedKnown := true
		dep, derr := host.GetLatestDeployment(ctx, dt.Owner, dt.Repo, dt.Environment)
		switch {
		case errors.Is(derr, code.ErrNotFound):
			st.deployedTag = "" // known: never deployed
		case derr != nil:
			deployedKnown = false // couldn't determine
		default:
			path := pathByName[dt.RepoName]
			if releaseTagPattern.MatchString(dep.Ref) {
				st.deployedTag = dep.Ref
			} else if dep.SHA != "" {
				if tags, terr := g.TagsContaining(ctx, path, dep.SHA); terr == nil {
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
		out = append(out, st)
	}
	return out, nil
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

	rewind, err := ui.RunCardSteps([]ui.CardStep{{
		Card: ui.NewCard(ui.CardRunning, "deploy").
			Raw("Checking deployed versions..."),
		Run: func() error {
			s, e := resolveServiceDeployTargets(ctx, g, host, workspace, targets)
			states = s
			return e
		},
	}}, func() *ui.Card {
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
			// Deployable but deselected in the form.
			nSkip++
			rows = append(rows, row{st.target.Label, glyphOff, off(st.target.Label, "not selected")})
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
