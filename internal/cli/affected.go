package cli

import (
	"context"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
func resolveAffectedServices(ctx context.Context, g vcs.VCS) ([]AffectedResult, error) {
	repos, err := resolveActiveRepositories(ctx, nil)
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

	var results []AffectedResult
	for _, r := range repos {
		services := resolveRepoServiceNames(r.Name)
		if len(services) == 0 {
			continue
		}

		changed, err := g.ChangedFiles(ctx, r.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Name, err)
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

// printAffectedSummary displays the change detection results.
func printAffectedSummary(results []AffectedResult) {
	for _, r := range results {
		if !r.HasChanges && len(r.Skipped) > 0 {
			ui.Skip(fmt.Sprintf("%s: no changes, skipping (%s)",
				r.RepoName, strings.Join(r.Skipped, ", ")))
			continue
		}
		if len(r.Skipped) > 0 {
			ui.Complete(fmt.Sprintf("%s: %d of %d services affected",
				r.RepoName, len(r.Services), len(r.Services)+len(r.Skipped)))
			for _, s := range r.Services {
				ui.Item("deploy", s)
			}
			for _, s := range r.Skipped {
				ui.Item("skip", s)
			}
		} else if len(r.Services) > 0 {
			ui.Complete(fmt.Sprintf("%s: all services affected (%s)",
				r.RepoName, strings.Join(r.Services, ", ")))
		}
	}
}
