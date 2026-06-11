package cli

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
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

// resolveAffectedServices determines which services are affected by changes
// on each repository's current branch relative to the default branch. Repos
// with no changes have all their services excluded. Repos using the map
// config form get per-service path-prefix filtering.
//
// Pre-flight: checks for unpushed commits and offers to push (interactive)
// or aborts (non-interactive) so the diff matches what CI has seen.
func resolveAffectedServices(ctx context.Context, workspace string, g vcs.VCS) ([]AffectedResult, error) {
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}

	// --- Pre-flight: push check ---

	repoBranch := make(map[string]string, len(repos))
	var needsPush []unpushedRepo

	for _, r := range repos {
		branch, err := g.GetCurrentBranch(ctx, r.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: getting current branch: %w", r.Name, err)
		}
		repoBranch[r.Name] = branch
		n, err := g.UnpushedCommits(ctx, r.Path, branch)
		if err != nil {
			return nil, fmt.Errorf("%s: checking unpushed commits: %w", r.Name, err)
		}
		if n != 0 {
			needsPush = append(needsPush, unpushedRepo{repo: r, branch: branch, count: n})
		}
	}

	if len(needsPush) > 0 {
		if err := promptPushOrAbort(ctx, g, needsPush); err != nil {
			return nil, err
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

	// --- Change detection ---
	//
	// ChangedFiles runs a `git fetch` per repo to get an accurate
	// merge-base, so this loop can hang for seconds on network. Run
	// it under a spinner; the spinner card rewinds on success so
	// printAffectedSummary's real cards land in its place.

	var results []AffectedResult
	rewind, err := ui.RunCardRewindable("detecting affected services", func() error {
		for _, r := range repos {
			services := resolveRepoServiceNames(r.Name)
			if len(services) == 0 {
				continue
			}

			changed, err := g.ChangedFiles(ctx, r.Path)
			if err != nil {
				return fmt.Errorf("%s: %w", r.Name, err)
			}

			branch := repoBranch[r.Name]

			if len(changed) == 0 {
				results = append(results, AffectedResult{
					RepoName: r.Name,
					RepoPath: r.Path,
					Branch:   branch,
					Skipped:  services,
				})
				continue
			}

			// Check if per-service path filtering is configured (map form).
			pathMap := resolveServicePaths(r.Name)
			if pathMap == nil {
				// Phase 1: repo has changes → include all services.
				results = append(results, AffectedResult{
					RepoName:   r.Name,
					RepoPath:   r.Path,
					Branch:     branch,
					HasChanges: true,
					Services:   services,
				})
				continue
			}

			// Phase 2: per-service path-prefix matching.
			result := matchServicePaths(r.Name, services, changed, pathMap)
			result.RepoPath = r.Path
			result.Branch = branch
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if rewind != nil {
		rewind()
	}

	return results, nil
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

// emitDeploymentSources renders the Observe-phase group that
// precedes the deploy plan: a "Deploying From" parent card with one
// child per repo explaining why it is — or isn't — part of the
// deployment. The group title carries the "why are these repos
// here" context that root-level repo cards couldn't; children are
// indented under it and roll up into the parent's aggregate glyph.
//
// Child shapes:
//
//	✓  extracker · PR #2791 → pr-2791     (withPRs; services as
//	    deploy  tag-api                     continuation lines)
//	    ...
//	○  web · no changes
//	✗  repo · <error>                      (remote/API failure)
//	○  repo · no PR for branch "x"
//
// When withPRs is true each changed repo's child runs under a group
// spinner while the PR lookup happens, and the returned overrides
// map (service → "pr-N") + repoPR list feed the deploy action.
// When false (release path) the child headline is the affected
// summary and no host calls are made.
//
// The repo-level overlap with the plan card that follows is
// intentional: this section shows the derivation (evidence), the
// plan asserts the conclusion — same relationship terraform's
// refresh output has to its plan lines.
func emitDeploymentSources(ctx context.Context, results []AffectedResult, withPRs bool) (map[string]string, []repoPR, error) {
	var host code.Host
	if withPRs {
		h, err := newCodeHost()
		if err != nil {
			return nil, nil, fmt.Errorf("code host (needed for image overrides): %w", err)
		}
		host = h
	}

	overrides := make(map[string]string)
	var prs []repoPR

	ui.RunGroup("deploying from", func(g ui.Reporter) {
		for _, r := range results {
			label := ui.PreserveCase(r.RepoName)

			if !r.HasChanges {
				if len(r.Skipped) > 0 {
					g.SkipValue(label, "no changes")
				}
				continue
			}
			if len(r.Services) == 0 {
				g.SkipValue(label, "no services affected")
				continue
			}

			if !withPRs {
				g.CompleteValue(label, affectedValue(r, ""))
				continue
			}

			var (
				identity gh.RepositoryIdentity
				pr       code.PullRequest
			)
			err := g.Spinner(label, func() error {
				var e error
				identity, e = gh.ParseRemote(ctx, r.RepoPath)
				if e != nil {
					return e
				}
				pr, e = host.GetPRForBranch(ctx, identity.Owner, identity.Name, r.Branch)
				return e
			})
			if err != nil {
				g.FailValue(label, err.Error())
				continue
			}
			if pr.Number == 0 {
				g.SkipValue(label, fmt.Sprintf("no PR for branch %q", r.Branch))
				continue
			}

			tag := fmt.Sprintf("pr-%d", pr.Number)
			for _, svc := range r.Services {
				overrides[svc] = tag
			}
			prs = append(prs, repoPR{
				RepoName: r.RepoName,
				Branch:   r.Branch,
				Owner:    identity.Owner,
				Repo:     identity.Name,
				PR:       pr,
			})

			g.CompleteValue(label, affectedValue(r, fmt.Sprintf("PR #%d → %s", pr.Number, tag)))
		}
	})

	if len(overrides) == 0 {
		return nil, prs, nil
	}
	return overrides, prs, nil
}

// affectedValue composes the multi-line Card.Value for a deployment-
// source child: the headline (PR tag when resolved, otherwise the
// affected-count summary), then one muted continuation line per
// service. Continuation lines align under the value column via
// Card.Value's multi-line support.
func affectedValue(r AffectedResult, headline string) string {
	if headline == "" {
		if len(r.Skipped) > 0 {
			headline = fmt.Sprintf("%d of %d services affected",
				len(r.Services), len(r.Services)+len(r.Skipped))
		} else {
			headline = "all services affected"
		}
	}
	lines := make([]string, 0, 1+len(r.Services)+len(r.Skipped))
	lines = append(lines, headline)
	for _, s := range r.Services {
		lines = append(lines, "deploy  "+s)
	}
	for _, s := range r.Skipped {
		lines = append(lines, "skip    "+s)
	}
	return strings.Join(lines, "\n")
}
