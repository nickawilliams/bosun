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
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// repoReadiness captures one repo's pre-deploy git state for the
// "Workspace Readiness" section: whether local commits are on the remote
// and whether the working tree is dirty.
type repoReadiness struct {
	repo     Repository
	branch   string
	unpushed int  // -1 = never pushed, >0 = commits ahead, 0 = in sync
	dirty    bool // uncommitted working-tree changes
	pushed   bool // we pushed it during this run
}

// AffectedResult holds the change-detection outcome for a single repository.
type AffectedResult struct {
	RepoName   string
	RepoPath   string   // Absolute path to the repository.
	Branch     string   // Current branch name.
	HasChanges bool
	Services   []string // Services to deploy.
	Skipped    []string // Services excluded (for display).
}

// prepareAffectedRepos runs the interactive pre-flight ahead of
// change detection: resolves the workspace's repos + branches, checks
// for unpushed commits and offers to push (interactive) or aborts
// (non-interactive) so the diff matches what CI has seen, and warns
// about dirty working trees. Detection itself happens inside
// emitDeploymentSources' group so each repo's `git fetch` runs under
// a per-repo child spinner — prompts can't run inside the group's
// TUI program, which is why this half stays separate.
func prepareAffectedRepos(ctx context.Context, workspace string, g vcs.VCS) ([]Repository, map[string]string, error) {
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, nil, err
	}

	readiness, repoBranch, anyUnpushed, err := gatherRepoReadiness(ctx, g, repos)
	if err != nil {
		return nil, nil, err
	}
	if err := emitWorkspaceReadiness(ctx, g, readiness, anyUnpushed); err != nil {
		return nil, nil, err
	}

	return repos, repoBranch, nil
}

// gatherRepoReadiness collects each repo's pre-flight git state —
// current branch, unpushed-commit count, dirty working tree — in one
// pass (all local git calls, fast). The returned slice feeds
// emitWorkspaceReadiness; the repoBranch map is a name→branch
// convenience for callers; anyUnpushed short-circuits the push offer.
// Shared by the deploy flow (preview/release) and review.
func gatherRepoReadiness(ctx context.Context, g vcs.VCS, repos []Repository) ([]repoReadiness, map[string]string, bool, error) {
	repoBranch := make(map[string]string, len(repos))
	readiness := make([]repoReadiness, 0, len(repos))
	anyUnpushed := false

	for _, r := range repos {
		branch, err := g.GetCurrentBranch(ctx, r.Path)
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s: getting current branch: %w", r.Name, err)
		}
		repoBranch[r.Name] = branch
		n, err := g.UnpushedCommits(ctx, r.Path, branch)
		if err != nil {
			return nil, nil, false, fmt.Errorf("%s: checking unpushed commits: %w", r.Name, err)
		}
		dirty, _ := g.IsDirty(ctx, r.Path)
		if n != 0 {
			anyUnpushed = true
		}
		readiness = append(readiness, repoReadiness{
			repo: r, branch: branch, unpushed: n, dirty: dirty,
		})
	}

	return readiness, repoBranch, anyUnpushed, nil
}

// hasRemoteBranch reports whether this repo's branch exists on the
// remote — true when it was already pushed (unpushed != -1) or got
// pushed during this run. review uses it to decide whether a PR can
// be opened: a never-pushed branch has no remote head ref to base a
// PR on.
func (rr repoReadiness) hasRemoteBranch() bool {
	return rr.unpushed != -1 || rr.pushed
}

// emitWorkspaceReadiness renders the "Workspace Readiness" section: the
// consolidated pre-deploy git state for every workspace repo, with
// the push offer folded in. Replaces the previous separate pieces
// (push prompt + per-repo "Pushed" card + per-repo dirty warnings)
// with one section that follows the Services section's grammar —
// stable title through prompt/spinners/final card, one row per
// repo, worst-first aggregate glyph:
//
//	✓  extracker                       (in sync, clean)
//	✓  extracker · pushed 3 commits    (pushed during this run)
//	▲  legacy-api · 3 unpushed commits, uncommitted changes
//	▲  host-ui · not yet pushed
//
// Prompt structure: an answered question is never re-asked, and the
// two possible prompts ask about different things —
//
//   - Unpushed commits → one bulk Yes/No push offer (default Yes,
//     the safe action: local work ends up in the deploy) listing
//     the unpushed repos. The ANSWER consumes the caveat for gating
//     purposes: a "No" means those commits never trigger the
//     continue gate (the user already chose to proceed without
//     them), though the rows still show them.
//   - Dirty trees → Continue/Cancel confirm over the readiness
//     rows, Cancel focused. There's no fix-it action bosun could
//     offer for uncommitted work, so proceeding past it must be
//     deliberate. This fires regardless of the push answer — the
//     push offer never asked about dirty trees.
//
// Worst case (unpushed + dirty) is two prompts, but never the same
// question twice. Cancel aborts with ErrCancelled. Non-interactive
// runs proceed with the static card (warning rows only). A fully-
// ready workspace renders the static ✓ card with no prompts. Only
// an accepted push that then fails is an error.
func emitWorkspaceReadiness(ctx context.Context, g vcs.VCS, readiness []repoReadiness, anyUnpushed bool) error {
	if !isInteractive() {
		buildWorkspaceReadinessCard(readiness).Print()
		return nil
	}

	// --- Push offer (unpushed commits only) ---

	pushAccepted := false
	if anyUnpushed {
		mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
		normalStyle := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg)
		var repoLines []string
		for _, rr := range readiness {
			if rr.unpushed == 0 {
				continue
			}
			status := "not yet pushed"
			if rr.unpushed > 0 {
				status = fmt.Sprintf("%d unpushed commit(s)", rr.unpushed)
			}
			repoLines = append(repoLines, fmt.Sprintf("  %s %s %s",
				mutedStyle.Render(rr.repo.Name),
				mutedStyle.Render(ui.Palette.Dot),
				normalStyle.Render(status)))
		}

		promptContent := mutedStyle.Render("Do you want to push before continuing?") +
			"\n\n" + strings.Join(repoLines, "\n")

		headerRewind := ui.NewCard(ui.CardInput, "workspace readiness").Tight().PrintRewindable()
		confirmed := true
		if err := runForm(
			newConfirm().
				Title(promptContent).
				Affirmative("Yes").
				Negative("No").
				Value(&confirmed),
		); err != nil {
			return err
		}
		headerRewind()
		pushAccepted = confirmed
	}

	// Accepted pushes run as steps of one spinner program
	// (RunCardSteps — per-repo programs would flash blank at every
	// boundary) whose final frame is the gate or the static card.
	// No-push paths produce zero steps → plain print, no program.
	var steps []ui.CardStep
	if pushAccepted {
		statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
		for i := range readiness {
			rr := &readiness[i]
			if rr.unpushed == 0 {
				continue
			}
			spin := ui.NewCard(ui.CardRunning, "workspace readiness").
				Raw(statusMuted.Render("Pushing ") +
					ui.Keyword(rr.repo.Name) +
					statusMuted.Render("..."))
			steps = append(steps, ui.CardStep{
				Card: spin,
				Run: func() error {
					if err := g.Push(ctx, rr.repo.Path, rr.branch); err != nil {
						return fmt.Errorf("pushing %s: %w", rr.repo.Name, err)
					}
					rr.pushed = true
					return nil
				},
			})
		}
	}

	// --- Continue gate (dirty trees only — unpushed was answered) ---

	anyDirty := false
	for _, rr := range readiness {
		if rr.dirty {
			anyDirty = true
			break
		}
	}

	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if anyDirty {
			// Gate variant: rows + a blank connector row above the
			// Continue/Cancel buttons huh renders beneath.
			gate := buildWorkspaceReadinessCard(readiness)
			gate.Text("")
			return gate.Tight()
		}
		return buildWorkspaceReadinessCard(readiness)
	})
	if err != nil {
		// An accepted push failed — the failed step card is already
		// on screen.
		return err
	}

	if anyDirty {
		// The gate card was painted by the program's final frame (or
		// the zero-step plain print), so suppress the spacer manually
		// the way Tight-on-Print would have.
		ui.ClearSpacer()
		proceed := false
		if err := runForm(
			newConfirm().
				Affirmative("Continue").
				Negative("Cancel").
				Value(&proceed),
		); err != nil {
			return err
		}
		if !proceed {
			ui.RequestSpacer()
			return ErrCancelled
		}
		// Swap the gate variant (with its trailing spacer row) for
		// the clean static card.
		rewind()
		buildWorkspaceReadinessCard(readiness).Print()
	}
	return nil
}

// buildWorkspaceReadinessCard composes the final static "Workspace Readiness"
// card: one Item row per repo, ready rows bare ✓, caveat rows ▲ with
// the caveats joined into the muted value. Same row vocabulary as
// buildServicesCard.
func buildWorkspaceReadinessCard(readiness []repoReadiness) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphWarn := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("▲")

	sorted := append([]repoReadiness{}, readiness...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].repo.Name < sorted[j].repo.Name })

	type row struct {
		glyph, content string
	}
	rows := make([]row, 0, len(sorted))
	state := ui.CardSuccess

	for _, rr := range sorted {
		var caveats []string
		if rr.pushed {
			n := rr.unpushed
			switch {
			case n == 1:
				caveats = append(caveats, "pushed 1 commit")
			case n > 1:
				caveats = append(caveats, fmt.Sprintf("pushed %d commits", n))
			default:
				caveats = append(caveats, "pushed")
			}
		} else if rr.unpushed != 0 {
			switch {
			case rr.unpushed < 0:
				caveats = append(caveats, "not yet pushed")
			case rr.unpushed == 1:
				caveats = append(caveats, "1 unpushed commit")
			default:
				caveats = append(caveats, fmt.Sprintf("%d unpushed commits", rr.unpushed))
			}
		}
		if rr.dirty {
			caveats = append(caveats, "uncommitted changes")
		}

		warn := (rr.unpushed != 0 && !rr.pushed) || rr.dirty
		glyph := glyphOK
		if warn {
			glyph = glyphWarn
			state = ui.CardSkipped
		}

		content := repoStyle.Render(rr.repo.Name)
		if len(caveats) > 0 {
			content += muted.Render(" · " + strings.Join(caveats, ", "))
		}
		rows = append(rows, row{glyph, content})
	}

	card := ui.NewCard(state, "workspace readiness")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
	}
	return card
}

// detectRepoAffected computes the change-detection outcome for one
// repo. The second return is false when the repo has no services
// configured (excluded from results entirely). ChangedFiles runs a
// `git fetch` for an accurate merge-base — callers should run this
// under a spinner.
func detectRepoAffected(ctx context.Context, g vcs.VCS, r Repository, branch string) (AffectedResult, bool, error) {
	services := resolveRepoServiceNames(r.Name)
	if len(services) == 0 {
		return AffectedResult{}, false, nil
	}

	changed, err := g.ChangedFiles(ctx, r.Path)
	if err != nil {
		return AffectedResult{}, false, fmt.Errorf("%s: %w", r.Name, err)
	}

	if len(changed) == 0 {
		return AffectedResult{
			RepoName: r.Name,
			RepoPath: r.Path,
			Branch:   branch,
			Skipped:  services,
		}, true, nil
	}

	// Per-service path filtering when configured (map form);
	// otherwise any change includes all services.
	pathMap := resolveServicePaths(r.Name)
	if pathMap == nil {
		return AffectedResult{
			RepoName:   r.Name,
			RepoPath:   r.Path,
			Branch:     branch,
			HasChanges: true,
			Services:   services,
		}, true, nil
	}

	result := matchServicePaths(r.Name, services, changed, pathMap)
	result.RepoPath = r.Path
	result.Branch = branch
	return result, true, nil
}

// resolveServicePaths returns the path-prefix map from the services config
// for repos using the map form. Returns nil if the repo uses string or list
// form (no per-service path filtering).
func resolveServicePaths(repoName string) map[string][]string {
	key := "services." + repoName
	raw := viper.Get(key)

	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	paths := make(map[string][]string, len(m))
	for k, v := range m {
		switch val := v.(type) {
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok {
					paths[k] = append(paths[k], s)
				}
			}
		case string:
			paths[k] = []string{val}
		}
	}
	return paths
}

// matchServicePaths performs per-service path-prefix matching against
// changed files. The _shared key triggers all services when matched.
func matchServicePaths(repoName string, services, changed []string, pathMap map[string][]string) AffectedResult {
	// Check _shared triggers first.
	if sharedPaths, ok := pathMap["_shared"]; ok {
		if anyPathMatches(changed, sharedPaths) {
			return AffectedResult{
				RepoName:   repoName,
				HasChanges: true,
				Services:   services,
			}
		}
	}

	var affected, skipped []string
	for _, svc := range services {
		prefixes, ok := pathMap[svc]
		if !ok {
			// Service has no path config — include conservatively.
			affected = append(affected, svc)
			continue
		}
		if anyPathMatches(changed, prefixes) {
			affected = append(affected, svc)
		} else {
			skipped = append(skipped, svc)
		}
	}

	return AffectedResult{
		RepoName:   repoName,
		HasChanges: len(affected) > 0,
		Services:   affected,
		Skipped:    skipped,
	}
}

// anyPathMatches returns true if any changed file matches any of the given
// path prefixes. A prefix ending with "/" matches any file under that
// directory. A prefix without "/" matches the exact file path.
func anyPathMatches(changed []string, prefixes []string) bool {
	for _, f := range changed {
		for _, p := range prefixes {
			if strings.HasSuffix(p, "/") {
				if strings.HasPrefix(f, p) {
					return true
				}
			} else {
				if f == p {
					return true
				}
			}
		}
	}
	return false
}

// sourceRepo bundles one repo's detection + PR-lookup outcome while
// emitDeploymentSources moves between its phases.
type sourceRepo struct {
	res      AffectedResult
	identity gh.RepositoryIdentity
	pr       code.PullRequest
	prErr    error
}

// detFail records a repo whose change detection errored — rendered
// as a ✗ row in the final Services card.
type detFail struct {
	repo string
	err  error
}

// prResolved reports whether this repo's services can be folded into
// a deployment when PR resolution applies: the lookup succeeded and a
// PR exists for the branch. Always true when withPRs is false (the
// release path needs no image tags). Note this deliberately does NOT
// require HasChanges — an unchanged repo with a live PR is still
// deployable, which is what makes redeploy-after-external-cleanup
// expressible through the selection form.
func (sr sourceRepo) prResolved(withPRs bool) bool {
	return !withPRs || (sr.prErr == nil && sr.pr.Number > 0)
}

// emitDeploymentSources renders the Observe-phase "Services" section
// that precedes the deploy plan, in three phases:
//
//  1. Detection + PR lookup. All repos run as steps of ONE spinner
//     program (RunCardSteps) — stable "Services" title, per-repo
//     status on the muted body line — whose final frame morphs into
//     phase 2's form header or phase 3's card, so the whole cycle
//     has zero TUI program boundaries (no blank-frame seams between
//     repos). Prompts can't run during this phase, which is why
//     prepareAffectedRepos (push offer, readiness gate) stays a
//     separate pre-flight step.
//
//  2. Optional selection form. When interactive, not auto-approved
//     (-y), and the caller passed no --service values, a multi-select
//     opens pre-checked to detection's picks — affected services
//     checked, everything else (path-map skips AND services of
//     unchanged repos) listed unchecked. Unchanged repos stay
//     toggleable on purpose: a preview env that was externally
//     cleaned up needs a redeploy with zero new commits, and the
//     form is where that intent gets expressed. One Enter accepts
//     detection as-is. Only repos without a resolvable PR (withPRs)
//     are excluded from toggling.
//
//  3. Final static card. The flat, alphabetically-sorted repo·service
//     list (single-service repos show just the repo name), rendered
//     as one Card with Item rows — a plain print, so there's no TUI
//     program boundary (and no blank-frame seam) anywhere in the
//     sequence:
//
//	✓  extracker · activity-api      (deploying)
//	▲  extracker · pdfgen            (excluded — path map or toggle)
//	▲  web · no changes
//	▲  repo · no PR for branch "x"
//	✗  repo · <error>                (detection or remote failure)
//
// Returns the (selection-adjusted) detection results for the caller's
// services list, the overrides map (service → "pr-N", withPRs only),
// and the repoPR list feeding the deploy action. A detection failure
// renders a ✗ row and surfaces as the returned error; PR-lookup
// failures render a ✗ row but stay non-fatal. Cancelling the form
// aborts with the form's error.
func emitDeploymentSources(ctx context.Context, cmd *cobra.Command, g vcs.VCS, repos []Repository, repoBranch map[string]string, withPRs bool) ([]AffectedResult, map[string]string, []repoPR, error) {
	var host code.Host
	if withPRs {
		h, err := newCodeHost()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("code host (needed for image overrides): %w", err)
		}
		host = h
	}

	// --- Phase 1: detection + PR lookup, one program over all repos ---

	var sources []sourceRepo
	var detFails []detFail
	var firstDetErr error

	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	steps := make([]ui.CardStep, 0, len(repos))
	for _, repo := range repos {
		spin := ui.NewCard(ui.CardRunning, "services").
			Raw(statusMuted.Render("Detecting changes in ") +
				ui.Keyword(repo.Name) +
				statusMuted.Render("..."))
		steps = append(steps, ui.CardStep{
			Card: spin,
			// Errors ride on detErr/prErr rather than the step's error
			// path — a failed repo becomes a ✗ row in the final card
			// and the sequence continues to the remaining repos.
			Run: func() error {
				var (
					sr      sourceRepo
					tracked bool
					detErr  error
				)
				sr.res, tracked, detErr = detectRepoAffected(ctx, g, repo, repoBranch[repo.Name])
				if detErr != nil {
					if firstDetErr == nil {
						firstDetErr = detErr
					}
					detFails = append(detFails, detFail{repo.Name, detErr})
					return nil
				}
				if !tracked {
					return nil
				}
				// PR lookup runs for unchanged repos too — their services
				// are toggleable in the selection form (redeploy after an
				// env was externally cleaned up), and a toggled-on service
				// needs its pr-N tag just like a changed one.
				if withPRs {
					sr.identity, sr.prErr = gh.ParseRemote(ctx, repo.Path)
					if sr.prErr == nil {
						sr.pr, sr.prErr = host.GetPRForBranch(ctx, sr.identity.Owner, sr.identity.Name, sr.res.Branch)
					}
				}
				sources = append(sources, sr)
				return nil
			},
		})
	}

	// --- Phase 2: optional selection form ---

	type toggle struct {
		src      int // index into sources
		svc      string
		selected bool
	}
	buildToggles := func() []toggle {
		var toggles []toggle
		for i, sr := range sources {
			if !sr.prResolved(withPRs) {
				continue
			}
			// Services = detection's picks (pre-checked); Skipped = the
			// rest, unchecked but toggleable. For unchanged repos every
			// service sits in Skipped, so they appear unchecked — checking
			// one expresses "redeploy this even though nothing changed".
			for _, svc := range sr.res.Services {
				toggles = append(toggles, toggle{i, svc, true})
			}
			for _, svc := range sr.res.Skipped {
				toggles = append(toggles, toggle{i, svc, false})
			}
		}
		return toggles
	}

	flagServices, _ := cmd.Flags().GetStringSlice("service")
	formGate := func() bool {
		return len(buildToggles()) > 0 && len(flagServices) == 0 &&
			isInteractive() && !isAutoApprove(cmd)
	}

	// The spinner program's final frame morphs into whatever comes
	// next — the selection form's header when the form will show, the
	// final Services card otherwise — so the program's exit paints
	// content instead of clearing to blank.
	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if formGate() {
			return ui.NewCard(ui.CardInput, "services").Tight()
		}
		return buildServicesCard(sources, detFails, withPRs)
	})
	if err != nil {
		return nil, nil, nil, err
	}

	formShown := formGate()
	if formShown {
		toggles := buildToggles()
		sort.SliceStable(toggles, func(i, j int) bool {
			ri, rj := sources[toggles[i].src].res.RepoName, sources[toggles[j].src].res.RepoName
			if ri != rj {
				return ri < rj
			}
			return toggles[i].svc < toggles[j].svc
		})

		// Bold the repo segment so it visually separates from the
		// service name (same color otherwise). Raw SGR bold-on/off
		// (1/22) rather than a lipgloss render: lipgloss closes with a
		// full reset, which would wipe huh's own selection/focus
		// styling for the rest of the line; intensity toggles compose
		// with whatever foreground huh applies.
		opts := make([]huh.Option[string], len(toggles))
		for i, t := range toggles {
			r := sources[t.src].res
			label := "\x1b[1m" + r.RepoName + "\x1b[22m"
			if len(r.Services)+len(r.Skipped) > 1 {
				label += " · " + t.svc
			}
			opts[i] = huh.NewOption(label, strconv.Itoa(i)).Selected(t.selected)
		}

		var picked []string
		// The header was painted by the spinner program's final frame
		// (not via Print), so suppress the spacer manually the way
		// Tight-on-Print would have.
		ui.ClearSpacer()
		// Full height — no viewport cap. The submitted form is
		// replaced by the final Services card listing the same rows,
		// so matching the form's height to the list makes the
		// swap read as in-place rather than an expand/collapse.
		if err := runForm(
			huh.NewMultiSelect[string]().
				Options(opts...).
				Height(len(opts)).
				Value(&picked),
		); err != nil {
			ui.RequestSpacer()
			return nil, nil, nil, err
		}

		pickedSet := make(map[int]bool, len(picked))
		for _, p := range picked {
			if i, err := strconv.Atoi(p); err == nil {
				pickedSet[i] = true
			}
		}
		// Rebuild each deployable repo's Services/Skipped from the
		// selection, preserving detection order within each list.
		chosen := make(map[int]map[string]bool, len(sources))
		for i, t := range toggles {
			if chosen[t.src] == nil {
				chosen[t.src] = make(map[string]bool)
			}
			chosen[t.src][t.svc] = pickedSet[i]
		}
		for src, sel := range chosen {
			r := &sources[src].res
			all := append(append([]string{}, r.Services...), r.Skipped...)
			r.Services = r.Services[:0]
			r.Skipped = r.Skipped[:0]
			for _, svc := range all {
				if sel[svc] {
					r.Services = append(r.Services, svc)
				} else {
					r.Skipped = append(r.Skipped, svc)
				}
			}
		}
	}

	// --- Phase 3: final card + outputs ---

	var results []AffectedResult
	overrides := make(map[string]string)
	var prs []repoPR
	for _, sr := range sources {
		results = append(results, sr.res)
		if withPRs && sr.prResolved(withPRs) && len(sr.res.Services) > 0 {
			tag := fmt.Sprintf("pr-%d", sr.pr.Number)
			for _, svc := range sr.res.Services {
				overrides[svc] = tag
			}
			prs = append(prs, repoPR{
				RepoName: sr.res.RepoName,
				Branch:   sr.res.Branch,
				Owner:    sr.identity.Owner,
				Repo:     sr.identity.Name,
				PR:       sr.pr,
			})
		}
	}

	if formShown {
		// The no-form path's final card was already painted by the
		// spinner program's final frame; the form path erases its
		// header and prints the selection-adjusted card.
		rewind()
		buildServicesCard(sources, detFails, withPRs).Print()
	}

	if len(overrides) == 0 {
		return results, nil, prs, firstDetErr
	}
	return results, overrides, prs, firstDetErr
}

// buildServicesCard composes the final static "Services" card: the
// flat sorted repo·service list as Item rows, with the card glyph
// aggregated worst-first (fail > skipped > success) the same way
// group parents aggregate their children.
//
// Row severity follows what the row means, not just "included or
// not": excluded services and no-changes repos are a normal,
// intentional outcome — they render fully de-emphasized (muted ○
// glyph, muted text), not as warnings. The ▲ warning glyph is
// reserved for rows the user might *expect* to deploy but that
// can't (no PR for the branch); ✗ for actual failures.
func buildServicesCard(sources []sourceRepo, detFails []detFail, withPRs bool) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphOff := muted.Render("○")
	glyphWarn := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("▲")
	glyphFail := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render("✗")

	type row struct {
		repo, svc, glyph, content string
	}
	var rows []row
	var nOK, nSkip, nFail int

	pair := func(r AffectedResult, svc string) string {
		if len(r.Services)+len(r.Skipped) == 1 {
			return repoStyle.Render(r.RepoName)
		}
		return repoStyle.Render(r.RepoName) + muted.Render(" · "+svc)
	}
	// pairOff/noteOff: the fully-muted variants for not-included
	// rows — the repo segment drops its primary color so the whole
	// row recedes.
	pairOff := func(r AffectedResult, svc string) string {
		if len(r.Services)+len(r.Skipped) == 1 {
			return muted.Render(r.RepoName)
		}
		return muted.Render(r.RepoName + " · " + svc)
	}
	note := func(repoName, text string) string {
		return repoStyle.Render(repoName) + muted.Render(" · "+text)
	}
	noteOff := func(repoName, text string) string {
		return muted.Render(repoName + " · " + text)
	}

	for _, f := range detFails {
		nFail++
		rows = append(rows, row{f.repo, "", glyphFail, note(f.repo, f.err.Error())})
	}
	for _, sr := range sources {
		r := sr.res
		switch {
		case withPRs && len(r.Services) == 0 && sr.prErr != nil:
			// PR lookup failed — surface it regardless of whether the
			// repo had changes. An unchanged repo whose lookup errored
			// would otherwise mask as a plain "no changes" row and
			// silently keep itself out of the selection form (a PR is
			// what makes a repo selectable). ✗ so the user can retry.
			nFail++
			rows = append(rows, row{r.RepoName, "", glyphFail, note(r.RepoName, sr.prErr.Error())})
		case !r.HasChanges && len(r.Services) == 0:
			// Unchanged and nothing toggled on — one compact receded
			// row instead of a ○ row per service. When the user DID
			// toggle services on (redeploy without changes), the
			// default branch renders the per-service pairs instead.
			// A "no PR" note when the branch has none, since under
			// PR-backed selection that's why it wasn't offered.
			nSkip++
			msg := "no changes"
			if withPRs && sr.pr.Number == 0 {
				msg = "no changes · no PR"
			}
			rows = append(rows, row{r.RepoName, "", glyphOff, noteOff(r.RepoName, msg)})
		case withPRs && r.HasChanges && sr.pr.Number == 0:
			nSkip++
			rows = append(rows, row{r.RepoName, "", glyphWarn, note(r.RepoName, fmt.Sprintf("no PR for branch %q", r.Branch))})
		default:
			for _, svc := range r.Services {
				nOK++
				rows = append(rows, row{r.RepoName, svc, glyphOK, pair(r, svc)})
			}
			for _, svc := range r.Skipped {
				nSkip++
				rows = append(rows, row{r.RepoName, svc, glyphOff, pairOff(r, svc)})
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].repo != rows[j].repo {
			return rows[i].repo < rows[j].repo
		}
		return rows[i].svc < rows[j].svc
	})

	state := ui.CardSuccess
	switch {
	case nFail > 0:
		state = ui.CardFailed
	case nSkip > 0 && nOK == 0:
		state = ui.CardSkipped
	}

	card := ui.NewCard(state, "services")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
	}
	return card
}
