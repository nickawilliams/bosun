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
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/spf13/cobra"
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
	RepoPath   string // Absolute path to the repository.
	Branch     string // Current branch name.
	HasChanges bool
	Services   []string // Services to deploy.
	Skipped    []string // Services excluded (for display).
	// StaleRemote marks a failed pre-diff fetch: detection ran against
	// whatever origin/<default> the local clone last saw, so the diff
	// may be stale. Rendered as a ▲ note on the repo's card rows.
	StaleRemote bool
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

	_, repoBranch, err := emitWorkspaceReadiness(ctx, g, repos)
	if err != nil {
		return nil, nil, err
	}

	return repos, repoBranch, nil
}

// hasRemoteBranch reports whether this repo's branch exists on the
// remote — true when it was already pushed (unpushed != -1) or got
// pushed during this run. review uses it to decide whether a PR can
// be opened: a never-pushed branch has no remote head ref to base a
// PR on.
func (rr repoReadiness) hasRemoteBranch() bool {
	return rr.unpushed != -1 || rr.pushed
}

// emitWorkspaceReadiness gathers each repo's pre-deploy git state
// (current branch, unpushed-commit count, dirty tree) under a per-repo
// "Checking …" spinner, then renders the "Workspace Readiness" section
// — the consolidated state for every workspace repo, with the push
// offer folded in. The gather and render phases share a single section
// title so the whole flow follows the Services section's grammar —
// stable title through gather/prompt/spinners/final card, one row per
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
func emitWorkspaceReadiness(ctx context.Context, g vcs.VCS, repos []Repository) ([]repoReadiness, map[string]string, error) {
	readiness := make([]repoReadiness, len(repos))
	repoBranch := make(map[string]string, len(repos))

	// Raw / non-interactive mode: gather sequentially, print the
	// static summary, return. No spinners, no prompts.
	if !isInteractive() {
		for i := range repos {
			r := repos[i]
			if rr, err := gatherRepoReadiness(ctx, g, r); err != nil {
				return nil, nil, err
			} else {
				readiness[i] = rr
				repoBranch[r.Name] = rr.branch
			}
		}
		buildWorkspaceReadinessCard(readiness).Print()
		return readiness, repoBranch, nil
	}

	// Interactive: RunGroup with a per-repo Spinner child that resolves
	// to a result row showing the repo's branch state. Mirrors the
	// cleanup-readiness pattern.
	var gatherErr error
	ui.RunGroup("workspace readiness", func(grp ui.Reporter) {
		for i := range repos {
			r := repos[i]
			err := grp.Spinner(ui.PreserveCase(r.Name), func() error {
				rr, err := gatherRepoReadiness(ctx, g, r)
				if err != nil {
					return err
				}
				readiness[i] = rr
				repoBranch[r.Name] = rr.branch
				return nil
			})
			if err != nil {
				if gatherErr == nil {
					gatherErr = err
				}
				grp.FailValue(ui.PreserveCase(r.Name), err.Error())
				continue
			}
			emitReadinessRow(grp, ui.PreserveCase(r.Name), readiness[i])
		}
	})
	if gatherErr != nil {
		return nil, nil, gatherErr
	}

	var anyUnpushed, anyDirty bool
	for _, rr := range readiness {
		if rr.unpushed != 0 {
			anyUnpushed = true
		}
		if rr.dirty {
			anyDirty = true
		}
	}
	if !anyUnpushed && !anyDirty {
		return readiness, repoBranch, nil
	}

	// Push offer via Dialog — interventional, but Dialog fits because
	// the question reduces to "perform this action, yes/no?". Default
	// to Push (the safer answer: local work ends up on the remote).
	pushAccepted := false
	if anyUnpushed {
		confirmed, err := NewDialog("Push to remote?").
			Description("Some branches have unpushed commits. Push them before continuing?").
			Affirmative("Push").
			Negative("Skip").
			Default(true).
			Show()
		if err != nil {
			return nil, nil, err
		}
		pushAccepted = confirmed
	}

	// Push action runs as its own RunGroup so each repo's push gets a
	// per-repo spinner-then-result row alongside the gather group.
	if pushAccepted {
		var pushErr error
		ui.RunGroup("pushing", func(grp ui.Reporter) {
			for i := range readiness {
				rr := &readiness[i]
				if rr.unpushed == 0 {
					continue
				}
				err := grp.Spinner(ui.PreserveCase(rr.repo.Name), func() error {
					return g.Push(ctx, rr.repo.Path, rr.branch)
				})
				label := ui.PreserveCase(rr.repo.Name)
				if err != nil {
					if pushErr == nil {
						pushErr = fmt.Errorf("pushing %s: %w", rr.repo.Name, err)
					}
					grp.FailValue(label, err.Error())
					continue
				}
				rr.pushed = true
				grp.Complete(label)
			}
		})
		if pushErr != nil {
			return nil, nil, pushErr
		}
	}

	// Dirty Dialog runs whenever any worktree is dirty — independent
	// of whether a push happened. The push offer never asked about
	// dirty trees, so this gate stands on its own.
	if anyDirty {
		confirmed, err := NewDialog("Warning").
			Description("Not all readiness checks passed, continue anyway?").
			Affirmative("Continue").
			Negative("Cancel").
			Default(false).
			Show()
		if err != nil {
			return nil, nil, err
		}
		if !confirmed {
			return nil, nil, ErrCancelled
		}
	}
	return readiness, repoBranch, nil
}

// gatherRepoReadiness runs the per-repo git probes that the readiness
// gather phase needs: current branch, unpushed commit count, dirty
// worktree state. Pulled out so both the raw-mode and interactive
// paths share one probe implementation.
func gatherRepoReadiness(ctx context.Context, g vcs.VCS, r Repository) (repoReadiness, error) {
	branch, err := g.GetCurrentBranch(ctx, r.Path)
	if err != nil {
		return repoReadiness{}, fmt.Errorf("%s: getting current branch: %w", r.Name, err)
	}
	n, err := g.UnpushedCommits(ctx, r.Path, branch)
	if err != nil {
		return repoReadiness{}, fmt.Errorf("%s: checking unpushed commits: %w", r.Name, err)
	}
	dirty, _ := g.IsDirty(ctx, r.Path)
	return repoReadiness{repo: r, branch: branch, unpushed: n, dirty: dirty}, nil
}

// emitReadinessRow renders one repo's readiness state into a parent
// group: ✓ when clean, ▲ with a comma-joined caveat list otherwise.
// Shape mirrors buildWorkspaceReadinessCard's per-row logic so the
// raw-mode card and the interactive group rows stay consistent.
func emitReadinessRow(grp ui.Reporter, label string, rr repoReadiness) {
	caveats := readinessCaveats(rr)
	if len(caveats) == 0 {
		grp.Complete(label)
		return
	}
	grp.SkipValue(label, strings.Join(caveats, ", "))
}

// readinessCaveats composes the muted-value caveat list shown next to
// a repo's readiness row when its state isn't clean. Shared between
// the interactive group emission and buildWorkspaceReadinessCard.
func readinessCaveats(rr repoReadiness) []string {
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
	return caveats
}

// buildWorkspaceReadinessCard composes the final static "Workspace Readiness"
// card: one Item row per repo, ready rows bare ✓, caveat rows ▲ with
// the caveats joined into the muted value. Same row vocabulary as
// buildServicesCard.
func buildWorkspaceReadinessCard(readiness []repoReadiness) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)
	glyphWarn := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render(ui.Palette.Attention)

	sorted := append([]repoReadiness{}, readiness...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].repo.Name < sorted[j].repo.Name })

	type row struct {
		glyph, content string
	}
	rows := make([]row, 0, len(sorted))
	state := ui.CardSuccess

	for _, rr := range sorted {
		caveats := readinessCaveats(rr)

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
// configured (excluded from results entirely). Resolves the default
// branch and fetches it for an accurate merge-base, then diffs against
// it — callers should run this under a spinner.
func detectRepoAffected(ctx context.Context, g vcs.VCS, r Repository, branch string) (AffectedResult, bool, error) {
	if !repoHasServices(r) {
		return AffectedResult{}, false, nil
	}
	services := resolveRepoServiceNames(r)

	defaultBranch, err := g.GetDefaultBranch(ctx, r.Path)
	if err != nil {
		return AffectedResult{}, false, fmt.Errorf("%s: getting default branch: %w", r.Name, err)
	}
	// Fetch latest to ensure an accurate merge-base for the diff. A
	// failed fetch (offline, auth) still detects against the local
	// origin ref, but marks the result so the card says the diff may
	// be stale instead of silently presenting it as current.
	staleRemote := g.Fetch(ctx, r.Path, "origin", defaultBranch) != nil

	changed, err := g.ChangedFiles(ctx, r.Path, "origin/"+defaultBranch)
	if err != nil {
		return AffectedResult{}, false, fmt.Errorf("%s: %w", r.Name, err)
	}

	if len(changed) == 0 {
		return AffectedResult{
			RepoName:    r.Name,
			RepoPath:    r.Path,
			Branch:      branch,
			Skipped:     services,
			StaleRemote: staleRemote,
		}, true, nil
	}

	// Per-service path filtering when configured (map form);
	// otherwise any change includes all services.
	pathMap := resolveServicePaths(r)
	if pathMap == nil {
		return AffectedResult{
			RepoName:    r.Name,
			RepoPath:    r.Path,
			Branch:      branch,
			HasChanges:  true,
			Services:    services,
			StaleRemote: staleRemote,
		}, true, nil
	}

	result := matchServicePaths(r.Name, services, changed, pathMap)
	result.RepoPath = r.Path
	result.Branch = branch
	result.StaleRemote = staleRemote
	return result, true, nil
}

// resolveServicePaths returns the path-prefix map from the services config
// for repos using the map form. Returns nil if the repo uses string or list
// form (no per-service path filtering).
//
// Reads through the same repo-scoped resolution resolveRepoServiceNames
// uses, so the names and the paths can never come from different
// layers — a descriptor that redefines the services would otherwise
// have been narrowed by the central map's path prefixes.
func resolveServicePaths(r Repository) map[string][]string {
	raw := loadRepoConfig(r).repoKeyed(servicesConfigGroup)

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
	identity code.RepositoryIdentity
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

// resolveDeploymentSource runs one repo's gather work: change
// detection, then (withPRs) the identity + PR lookup its image
// override needs. Pulled out of the gather step so the step reads as
// resolve → render, mirroring prerelease's resolveReleaseTarget.
//
// The second return is false when the repo has no services (it
// contributes nothing to the Services section). The gather filters
// those repos out ahead of time, so this is a guard against the two
// rules disagreeing rather than a path taken — both ask
// repoHasServices. A non-nil error is a detection failure: it becomes
// a ✗ row and the caller's returned error, but never stops the
// remaining repos. PR-lookup failures ride on the returned
// sourceRepo's prErr instead (non-fatal).
//
// The PR lookup runs for unchanged repos too — their services are
// toggleable in the selection form (redeploy after an env was
// externally cleaned up), and a toggled-on service needs its pr-N tag
// just like a changed one.
func resolveDeploymentSource(ctx context.Context, g vcs.VCS, host code.Host, repo Repository, branch string, withPRs bool) (sourceRepo, bool, error) {
	var sr sourceRepo
	res, tracked, err := detectRepoAffected(ctx, g, repo, branch)
	if err != nil {
		return sr, false, err
	}
	if !tracked {
		return sr, false, nil
	}
	sr.res = res
	if withPRs {
		sr.identity, sr.prErr = host.ParseRemote(ctx, repo.Path)
		if sr.prErr == nil {
			sr.pr, sr.prErr = host.GetPRForBranch(ctx, sr.identity.Owner, sr.identity.Name, sr.res.Branch)
		}
	}
	return sr, true, nil
}

// emitDeploymentSources renders the Observe-phase "Services" section
// that precedes the deploy plan, in three phases:
//
//  1. Detection + PR lookup. All repos run as steps of ONE spinner
//     program (RunCardStepsInto) whose cards accumulate: each step
//     carries the rows resolved so far plus a pending row for the
//     in-flight repo, so the Services list materializes in place and
//     the program's final frame hands straight off to phase 2's form
//     or phase 3's card — zero TUI program boundaries (no blank-frame
//     seams between repos). Prompts can't run during this phase,
//     which is why prepareAffectedRepos (push offer, readiness gate)
//     stays a separate pre-flight step.
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
//  3. Final static card. The same repo·service rows the gather
//     painted — repos alphabetically, sourceRows owning the order
//     within a repo, single-service repos showing just the repo name
//     — rendered as one Card with Item rows. A plain print, so there's
//     no TUI program boundary (and no blank-frame seam) anywhere in
//     the sequence:
//
//     ✓  extracker · activity-api      (deploying)
//     ○  extracker · pdfgen            (excluded — path map or toggle)
//     ○  web · no changes
//     ▲  repo · no PR for branch "x"
//     ✗  repo · <error>                (detection or remote failure)
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
	//
	// Accumulating gather (the release-command pattern): each step's
	// card carries the rows resolved so far plus a pending row — the
	// in-flight repo with the live spinner in its glyph slot — so the
	// list materializes in place and then swaps into the form (or the
	// record card) with the same rows. Rows render via sourceRows /
	// detFailRow, the record card's own renderers, so gather and record
	// can't drift. Unlike the sibling gathers a repo can contribute
	// more than one row (a stale-fetch caveat above its outcome, or one
	// row per service), which is why steps append a slice.
	//
	// Repos with no services (repoHasServices — the same rule detection
	// applies) are dropped before the gather rather than stepped
	// through: detection returns before its first git call for them and
	// they contribute no row, so a step would paint a pending row that
	// resolves into nothing and the list would shrink at the handoff.
	//
	// Mutating later cards from a step's Run is race-free: the runner's
	// worker sends each step's result over a channel before the model
	// advances to the next card (happens-before; see release_gate.go).

	var sources []sourceRepo
	var detFails []detFail
	var firstDetErr error

	gathered := make([]Repository, 0, len(repos))
	for _, repo := range repos {
		if repoHasServices(repo) {
			gathered = append(gathered, repo)
		}
	}

	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	n := len(gathered)
	pendingRow := func(c *ui.Card, i int) {
		c.Item(ui.GlyphSlot, statusMuted.Render(gathered[i].Name))
	}
	cards := make([]*ui.Card, n)
	for i := range cards {
		cards[i] = ui.NewCard(ui.CardRunning, "services")
	}
	if n > 0 {
		pendingRow(cards[0], 0)
	}

	steps := make([]ui.CardStep, 0, n)
	for i, repo := range gathered {
		steps = append(steps, ui.CardStep{
			Card: cards[i],
			// Errors ride on detErr/prErr rather than the step's error
			// path — a failed repo becomes a ✗ row in the final card
			// and the sequence continues to the remaining repos.
			Run: func() error {
				var rows []serviceRow
				sr, tracked, detErr := resolveDeploymentSource(ctx, g, host, repo, repoBranch[repo.Name], withPRs)
				switch {
				case detErr != nil:
					if firstDetErr == nil {
						firstDetErr = detErr
					}
					f := detFail{repo.Name, detErr}
					detFails = append(detFails, f)
					rows = []serviceRow{detFailRow(f)}
				case tracked:
					sources = append(sources, sr)
					// Gather-time inclusion IS detection's classification:
					// Services deploying, Skipped receded.
					rows = sourceRows(sr, withPRs)
				}
				for j := i + 1; j < n; j++ {
					for _, row := range rows {
						cards[j].Item(row.glyph, row.content)
					}
				}
				if i+1 < n {
					pendingRow(cards[i+1], i+1)
				}
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

	// The form is built lazily so its options reflect the gather's
	// results. Under the session shell it embeds directly beneath the
	// "services" input header in the same program — the takeover
	// machinery (formFirstFrame paint + cursor-up repaint) that used
	// to bridge the steps program into huh's program is gone, because
	// there is no boundary to bridge.
	var (
		picked  []string
		toggles []toggle
		msField *huh.MultiSelect[string]
	)
	buildSelectionForm := func() {
		if msField != nil {
			return
		}
		toggles = buildToggles()
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
		// Full height whenever it fits: the submitted form is replaced
		// by the final Services card listing the same rows, so matching
		// the form's height to the list makes the swap read as in-place
		// rather than an expand/collapse. fittedSelectHeight caps it to
		// the terminal — a frame taller than the screen breaks the
		// takeover below rather than just overflowing.
		msField = fittedMultiSelect(opts, &picked)
	}

	// Run the gather; its final card is the tail the next phase builds
	// on — the "services" input header when the selection form follows
	// (the form embeds directly beneath it, same program, no seam), or
	// the final Services card when no form will show. In raw mode the
	// input-state header stays silent and the form runs against the
	// harness's injected reader, exactly as before.
	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if formGate() {
			// Input-state successor: silent in raw mode (the record
			// card would misreport — the form hasn't adjusted the
			// selection yet), the form's header otherwise.
			return ui.NewCard(ui.CardInput, "services").Tight()
		}
		return buildServicesCard(sources, detFails, withPRs)
	})
	if err != nil {
		return nil, nil, nil, err
	}

	formShown := formGate()
	if formShown {
		buildSelectionForm()
		if err := runForm(msField); err != nil {
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
		if withPRs && !sr.prResolved(withPRs) && len(sr.res.Services) > 0 {
			// The card renders this repo as not-deployable ("no PR for
			// branch") and the form never offered its services — the
			// returned result set must agree, or preview would deploy
			// them with no pr-N override (i.e. the provider's default
			// image tag). Local mutation only: sr is a copy, so the
			// card built from sources is unaffected.
			sr.res.Skipped = append(append([]string{}, sr.res.Services...), sr.res.Skipped...)
			sr.res.Services = nil
		}
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
		// The submitted form has resolved; erase the input header (a
		// tail drop under the session shell) and put the
		// selection-adjusted Services card in its place. The no-form
		// path's final card is already the tail — nothing to do.
		rewind()
		ui.ClearSpacer()
		buildServicesCard(sources, detFails, withPRs).Print()
	}

	if len(overrides) == 0 {
		return results, nil, prs, firstDetErr
	}
	return results, overrides, prs, firstDetErr
}

// rowSeverity is what a Services row contributes to the card's
// aggregate glyph. rowNote rows (the stale-fetch caveat) are additive
// context about a repo whose real outcome another row states, so they
// count toward nothing.
type rowSeverity int

const (
	rowNote rowSeverity = iota
	rowOK
	rowSkip
	rowFail
)

// serviceRow is one row of the Services surface: the rendered glyph +
// content, and the severity it contributes to the card's aggregate
// state. Rows arrive in display order — sourceRows sorts before it
// renders — so nothing downstream needs the sort key back.
type serviceRow struct {
	glyph, content string
	sev            rowSeverity
}

// svcOn composes a Services row that stays in the foreground: the
// label in primary with an optional muted note after a separator. A
// blank note renders the label alone, no trailing " · ".
func svcOn(label, note string) string {
	primary := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	if note == "" {
		return primary.Render(label)
	}
	return primary.Render(label) + lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render(" · "+note)
}

// svcOff is svcOn's fully-muted variant for rows that aren't included
// — the label drops its primary color so the whole row recedes.
func svcOff(label, note string) string {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	if note == "" {
		return muted.Render(label)
	}
	return muted.Render(label + " · " + note)
}

// detFailRow renders a repo whose change detection errored: ✗ with the
// error as the row's note.
func detFailRow(f detFail) serviceRow {
	return serviceRow{
		glyph:   lipgloss.NewStyle().Foreground(ui.Palette.Error).Render(ui.Palette.Cross),
		content: svcOn(f.repo, f.err.Error()),
		sev:     rowFail,
	}
}

// sourceRows renders one repo's Services rows — the shared shape
// between the gather's progressively-filling card and the final record
// card, so the two can't drift. Inclusion is read from the result's
// Services/Skipped split: detection's classification during gather,
// the user's choice after the selection form. Rows come back in the
// card's own order (the stale caveat first, then services by name), so
// callers only have to order repos.
//
// Row severity follows what the row means, not just "included or
// not": excluded services and no-changes repos are a normal,
// intentional outcome — they render fully de-emphasized (muted ○
// glyph, muted text), not as warnings. The ▲ warning glyph is
// reserved for rows the user might *expect* to deploy but that
// can't (no PR for the branch); ✗ for actual failures.
func sourceRows(sr sourceRepo, withPRs bool) []serviceRow {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)
	glyphOff := muted.Render(ui.Palette.Inactive)
	glyphWarn := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render(ui.Palette.Attention)
	glyphFail := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render(ui.Palette.Cross)

	r := sr.res
	var rows []serviceRow
	if r.StaleRemote {
		rows = append(rows, serviceRow{
			glyph:   glyphWarn,
			content: svcOn(r.RepoName, "remote fetch failed — diff may be stale"),
			sev:     rowNote,
		})
	}

	switch {
	case withPRs && len(r.Services) == 0 && sr.prErr != nil:
		// PR lookup failed — surface it regardless of whether the
		// repo had changes. An unchanged repo whose lookup errored
		// would otherwise mask as a plain "no changes" row and
		// silently keep itself out of the selection form (a PR is
		// what makes a repo selectable). ✗ so the user can retry.
		rows = append(rows, serviceRow{
			glyph:   glyphFail,
			content: svcOn(r.RepoName, sr.prErr.Error()),
			sev:     rowFail,
		})
	case !r.HasChanges && len(r.Services) == 0:
		// Unchanged and nothing toggled on — one compact receded
		// row instead of a ○ row per service. When the user DID
		// toggle services on (redeploy without changes), the
		// default branch renders the per-service pairs instead.
		// A "no PR" note when the branch has none, since under
		// PR-backed selection that's why it wasn't offered.
		msg := "no changes"
		if withPRs && sr.pr.Number == 0 {
			msg = "no changes · no PR"
		}
		rows = append(rows, serviceRow{
			glyph:   glyphOff,
			content: svcOff(r.RepoName, msg),
			sev:     rowSkip,
		})
	case withPRs && r.HasChanges && sr.pr.Number == 0:
		rows = append(rows, serviceRow{
			glyph:   glyphWarn,
			content: svcOn(r.RepoName, fmt.Sprintf("no PR for branch %q", r.Branch)),
			sev:     rowSkip,
		})
	default:
		// One row per service, name-ordered across both lists so the
		// deploying and excluded rows interleave the way the record
		// card has always shown them.
		type pick struct {
			svc string
			on  bool
		}
		picks := make([]pick, 0, len(r.Services)+len(r.Skipped))
		for _, svc := range r.Services {
			picks = append(picks, pick{svc, true})
		}
		for _, svc := range r.Skipped {
			picks = append(picks, pick{svc, false})
		}
		sort.SliceStable(picks, func(i, j int) bool { return picks[i].svc < picks[j].svc })

		for _, p := range picks {
			// Single-service repos show just the repo name — the
			// service segment would only restate the row.
			note := p.svc
			if len(picks) == 1 {
				note = ""
			}
			if p.on {
				rows = append(rows, serviceRow{
					glyph:   glyphOK,
					content: svcOn(r.RepoName, note),
					sev:     rowOK,
				})
				continue
			}
			rows = append(rows, serviceRow{
				glyph:   glyphOff,
				content: svcOff(r.RepoName, note),
				sev:     rowSkip,
			})
		}
	}
	return rows
}

// buildServicesCard composes the final static "Services" card: every
// repo's rows (from the same renderers the gather paints), repos in
// name order, with the card glyph aggregated worst-first (fail >
// skipped > success) the same way group parents aggregate their
// children.
func buildServicesCard(sources []sourceRepo, detFails []detFail, withPRs bool) *ui.Card {
	// Grouped by repo rather than sorted row-by-row: the renderers own
	// the order within a repo, so the card can't reshuffle rows the
	// gather already painted in that order.
	type group struct {
		repo string
		rows []serviceRow
	}
	groups := make([]group, 0, len(detFails)+len(sources))
	for _, f := range detFails {
		groups = append(groups, group{f.repo, []serviceRow{detFailRow(f)}})
	}
	for _, sr := range sources {
		groups = append(groups, group{sr.res.RepoName, sourceRows(sr, withPRs)})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].repo < groups[j].repo })

	var rows []serviceRow
	var nOK, nSkip, nFail int
	for _, grp := range groups {
		for _, r := range grp.rows {
			switch r.sev {
			case rowOK:
				nOK++
			case rowSkip:
				nSkip++
			case rowFail:
				nFail++
			}
			rows = append(rows, r)
		}
	}

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
