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

type unpushedRepo struct {
	repo   Repository
	branch string
	count  int // -1 = never pushed, >0 = commits ahead
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

	// --- Pre-flight: push check ---

	repoBranch := make(map[string]string, len(repos))
	var needsPush []unpushedRepo

	for _, r := range repos {
		branch, err := g.GetCurrentBranch(ctx, r.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: getting current branch: %w", r.Name, err)
		}
		repoBranch[r.Name] = branch
		n, err := g.UnpushedCommits(ctx, r.Path, branch)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: checking unpushed commits: %w", r.Name, err)
		}
		if n != 0 {
			needsPush = append(needsPush, unpushedRepo{repo: r, branch: branch, count: n})
		}
	}

	if len(needsPush) > 0 {
		if err := promptPushOrAbort(ctx, g, needsPush); err != nil {
			return nil, nil, err
		}
	}

	// --- Dirty working tree warning ---

	for _, r := range repos {
		dirty, err := g.IsDirty(ctx, r.Path)
		if err != nil {
			continue
		}
		if dirty {
			ui.Skip(fmt.Sprintf("%s: uncommitted changes won't be reflected", r.Name))
		}
	}

	return repos, repoBranch, nil
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

// promptPushOrAbort prompts to push unpushed repos (interactive) or aborts
// (non-interactive). Mirrors the push-check pattern from review.go.
func promptPushOrAbort(ctx context.Context, g vcs.VCS, needsPush []unpushedRepo) error {
	if !isInteractive() {
		names := make([]string, len(needsPush))
		for i, up := range needsPush {
			names[i] = up.repo.Name
		}
		return fmt.Errorf(
			"unpushed commits in %s — push first or use --service to bypass detection",
			strings.Join(names, ", "),
		)
	}

	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	normalStyle := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg)
	var repoLines []string
	for _, up := range needsPush {
		status := "not yet pushed"
		if up.count > 0 {
			status = fmt.Sprintf("%d unpushed commit(s)", up.count)
		}
		repoLines = append(repoLines, fmt.Sprintf("  %s %s %s",
			mutedStyle.Render(up.repo.Name),
			mutedStyle.Render(ui.Palette.Dot),
			normalStyle.Render(status)))
	}

	promptContent := mutedStyle.Render("Do you want to push before continuing?") +
		"\n\n" + strings.Join(repoLines, "\n")

	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, "unpushed commits detected").Tight())

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
	if !confirmed {
		slot.Show(ui.NewCard(ui.CardSkipped, "push declined"))
		slot.Finalize()
		return fmt.Errorf("aborted: unpushed commits")
	}

	for _, up := range needsPush {
		if err := slot.Run(fmt.Sprintf("pushing %s", up.repo.Name), func() error {
			return g.Push(ctx, up.repo.Path, up.branch)
		}); err != nil {
			return fmt.Errorf("pushing %s: %w", up.repo.Name, err)
		}
	}
	pushedPairs := make([]string, 0, len(needsPush)*2)
	for _, up := range needsPush {
		pushedPairs = append(pushedPairs, up.repo.Name, up.branch)
	}
	slot.Show(ui.NewCard(ui.CardSuccess, "pushed").KV(pushedPairs...))
	slot.Finalize()

	return nil
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

// deployable reports whether this repo's services can actually be
// toggled into a deployment: it has changes + services, and (when PR
// resolution applies) the PR lookup succeeded.
func (sr sourceRepo) deployable(withPRs bool) bool {
	if !sr.res.HasChanges || len(sr.res.Services) == 0 {
		return false
	}
	if withPRs && (sr.prErr != nil || sr.pr.Number == 0) {
		return false
	}
	return true
}

// emitDeploymentSources renders the Observe-phase "Services" section
// that precedes the deploy plan, in three phases:
//
//  1. Detection + PR lookup. Each repo runs under a slot spinner
//     (stable "Services" title, repo name on the muted body line) —
//     ChangedFiles' per-repo `git fetch` and the GH PR lookup are
//     the slow parts. Prompts can't run during this phase, which is
//     why prepareAffectedRepos (push check, dirty warnings) stays a
//     separate pre-flight step.
//
//  2. Optional selection form. When interactive, not auto-approved
//     (-y), and the caller passed no --service values, a multi-select
//     opens pre-checked to detection's picks — affected services
//     checked, path-map-skipped services listed but unchecked. One
//     Enter accepts detection as-is; toggling adjusts what deploys.
//     Repos without changes or without a resolvable PR aren't
//     toggleable and appear only in the final list.
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

	// --- Phase 1: detection + PR lookup under per-repo slot spinners ---

	slot := ui.NewSlot()
	var sources []sourceRepo
	var detFails []detFail
	var firstDetErr error

	for _, repo := range repos {
		spin := ui.NewCard(ui.CardRunning, "services").Muted(repo.Name)
		var (
			sr      sourceRepo
			tracked bool
			detErr  error
		)
		// Errors ride on detErr/prErr rather than the slot's error
		// path — a failed repo becomes a ✗ row in the final card, not
		// a permanently-printed failure card mid-sequence.
		_ = slot.RunCard(spin, func() error {
			sr.res, tracked, detErr = detectRepoAffected(ctx, g, repo, repoBranch[repo.Name])
			if detErr != nil || !tracked {
				return nil
			}
			if withPRs && sr.res.HasChanges && len(sr.res.Services) > 0 {
				sr.identity, sr.prErr = gh.ParseRemote(ctx, repo.Path)
				if sr.prErr == nil {
					sr.pr, sr.prErr = host.GetPRForBranch(ctx, sr.identity.Owner, sr.identity.Name, sr.res.Branch)
				}
			}
			return nil
		})
		if detErr != nil {
			if firstDetErr == nil {
				firstDetErr = detErr
			}
			detFails = append(detFails, detFail{repo.Name, detErr})
			continue
		}
		if !tracked {
			continue
		}
		sources = append(sources, sr)
	}

	// --- Phase 2: optional selection form ---

	type toggle struct {
		src      int // index into sources
		svc      string
		selected bool
	}
	var toggles []toggle
	for i, sr := range sources {
		if !sr.deployable(withPRs) {
			continue
		}
		for _, svc := range sr.res.Services {
			toggles = append(toggles, toggle{i, svc, true})
		}
		for _, svc := range sr.res.Skipped {
			toggles = append(toggles, toggle{i, svc, false})
		}
	}

	flagServices, _ := cmd.Flags().GetStringSlice("service")
	if len(toggles) > 0 && len(flagServices) == 0 && isInteractive() && !isAutoApprove(cmd) {
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
		slot.Show(ui.NewCard(ui.CardInput, "services").Tight())
		if err := runForm(
			huh.NewMultiSelect[string]().
				Options(opts...).
				Height(min(len(opts)+1, maxSelectHeight)).
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
		if withPRs && sr.deployable(withPRs) && len(sr.res.Services) > 0 {
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

	slot.Show(buildServicesCard(sources, detFails, withPRs))
	slot.Finalize()

	if len(overrides) == 0 {
		return results, nil, prs, firstDetErr
	}
	return results, overrides, prs, firstDetErr
}

// buildServicesCard composes the final static "Services" card: the
// flat sorted repo·service list as Item rows, with the card glyph
// aggregated worst-first (fail > skipped > success) the same way
// group parents aggregate their children.
func buildServicesCard(sources []sourceRepo, detFails []detFail, withPRs bool) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphSkip := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("▲")
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
	note := func(repoName, text string) string {
		return repoStyle.Render(repoName) + muted.Render(" · "+text)
	}

	for _, f := range detFails {
		nFail++
		rows = append(rows, row{f.repo, "", glyphFail, note(f.repo, f.err.Error())})
	}
	for _, sr := range sources {
		r := sr.res
		switch {
		case !r.HasChanges && len(r.Skipped) > 0:
			nSkip++
			rows = append(rows, row{r.RepoName, "", glyphSkip, note(r.RepoName, "no changes")})
		case withPRs && r.HasChanges && len(r.Services)+len(r.Skipped) > 0 && sr.prErr != nil:
			nFail++
			rows = append(rows, row{r.RepoName, "", glyphFail, note(r.RepoName, sr.prErr.Error())})
		case withPRs && r.HasChanges && len(r.Services)+len(r.Skipped) > 0 && sr.pr.Number == 0:
			nSkip++
			rows = append(rows, row{r.RepoName, "", glyphSkip, note(r.RepoName, fmt.Sprintf("no PR for branch %q", r.Branch))})
		default:
			for _, svc := range r.Services {
				nOK++
				rows = append(rows, row{r.RepoName, svc, glyphOK, pair(r, svc)})
			}
			for _, svc := range r.Skipped {
				nSkip++
				rows = append(rows, row{r.RepoName, svc, glyphSkip, pair(r, svc)})
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
