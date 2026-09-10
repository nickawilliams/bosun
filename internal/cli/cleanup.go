package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/fsutil"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/services"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cleanup [pattern]",
		Short: "Tear down workspace previews, branches, and worktrees",
		Long: `Tear down workspace previews, branches, and worktrees.

The positional pattern selects which workspaces: an exact name targets
one workspace, a glob targets a batch ('*' matches within a path
segment, '**' crosses segments — quote it so the shell doesn't expand
it). Without a pattern, cleanup targets the workspace context (CWD or
--issue), or opens the batch picker when there is none.`,
		Args: cobra.MaximumNArgs(1),
		Annotations: map[string]string{
			headerAnnotationTitle: "clean up workspace",
		},
		RunE: shellRunE(func(cmd *cobra.Command, args []string) error {
			query, err := resolveWorkspaceQuery(cmd)
			if err != nil {
				return err
			}
			sel, err := resolveWorkspaceSelection(cmd, args, query, selectionDestructive)
			if err != nil {
				return err
			}
			if sel.batch {
				return runCleanupBulk(cmd, sel.pattern, query)
			}
			return runCleanupSingle(cmd, sel.exact)
		}),
	}

	setTitleResolver(cmd, func(cc CommandContext) string {
		if selectionLooksBatch(cmd, cc) {
			return "clean up workspaces"
		}
		return "" // fall back to the static annotation
	})

	addProjectFlag(cmd)
	addWorkspaceFlag(cmd)
	addIssueFlag(cmd)
	addAllFlag(cmd)
	addWorkspaceFilterFlags(cmd)
	cmd.Flags().Bool("force", false, "bypass cleanup-readiness blockers (uncommitted changes, unmerged work, stray files)")
	return cmd
}

// runCleanupSingle tears down one workspace, all-or-nothing: any
// readiness BLOCK aborts the run unless --force. The target is the
// exact-name pattern when one was given, otherwise the resolved
// workspace context — the selection pipeline guarantees one of the
// two exists by the time this runs.
func runCleanupSingle(cmd *cobra.Command, exact string) error {
	cc := commandContext(cmd)
	if exact != "" {
		// Positional name wins over the resolution chain — it's an
		// explicit on-the-line target (same rule as workspace delete).
		if err := validateWorkspaceExists(exact); err != nil {
			return err
		}
		cc.Workspace = exact
		if cc.Issue == "" {
			cc.Issue = workspaceIssueKey(exact)
		}
	}
	if err := cc.RequireWorkspace(); err != nil {
		return err
	}
	workspace := cc.Workspace
	force, _ := cmd.Flags().GetBool("force")
	ctx := cmd.Context()
	g := git.New()

	_, _ = emitLifecyclePreamble(ctx, cc.Issue)

	projectRepos, err := resolveRepositories(nil)
	if err != nil {
		return err
	}

	target, err := resolveCleanupTarget(ctx, workspace, cc.Issue, mainPathIndex(projectRepos))
	if err != nil {
		return err
	}

	// --- Pre-flight: Cleanup Readiness ---
	//
	// Workspace-specific safety check (parallel to readiness):
	// classifies each repo + the workspace itself across the
	// safety matrix and gates on the worst severity. BLOCK
	// findings (data loss) exit early unless --force; WARN
	// findings (status mismatches) prompt Continue/Cancel.
	host, _ := newCodeHost()
	tracker, _ := newIssueTracker()
	cleanupRepos, _, err := emitCleanupReadiness(ctx, g, host, tracker, target.repos, target.wsPath, cc.Issue, force)
	if err != nil {
		return err
	}

	// Resolve the actual branch each worktree is checked out on
	// (handles the "manually checked out a different branch"
	// case). emitCleanupReadiness already captured these; reuse
	// them.
	actualBranch := make(map[string]string, len(cleanupRepos))
	for _, rc := range cleanupRepos {
		actualBranch[rc.repo.Name] = rc.branch
	}

	moved := &movedShell{}
	actions := buildCleanupActions(ctx, g, target, actualBranch, force, moved)

	err = runActions(cmd, ctx, actions)
	moved.emit()
	return err
}

// runCleanupBulk tears down the workspaces the pattern (narrowed by
// the query) selects. Interactively, the readiness-annotated
// multi-select picker is the selection's interactive form: ready
// workspaces arrive preselected, WARN workspaces unselected
// (selecting one IS the acknowledgment), BLOCK workspaces are gated
// behind --force. Non-interactively the pattern/filter is the
// selection, with sweep semantics: a workspace that can't be
// evaluated, can't be resolved, or carries a readiness BLOCK is
// excluded and reported while the rest of the sweep proceeds
// (--force includes readiness blocks). One combined plan covers all
// included workspaces, gated by a single approval.
func runCleanupBulk(cmd *cobra.Command, pattern string, query workspaceQuery) error {
	ctx := cmd.Context()
	force, _ := cmd.Flags().GetBool("force")
	g := git.New()

	mgr, err := newWorkspaceManager()
	if err != nil {
		return err
	}
	names, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	if len(names) == 0 {
		ui.Skip("no workspaces found in project")
		return nil
	}
	names, err = matchWorkspaceNames(pattern, names)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		ui.Skip(fmt.Sprintf("no workspaces match %q", pattern))
		return nil
	}

	tracker, _ := newIssueTracker()
	host, _ := newCodeHost()

	// Resolving Workspaces — one card scoped over the whole
	// pre-readiness stretch: observe issue states (the filter's
	// input), filter, and resolve each match into a cleanup target.
	// The card resolves in place to a standing "n workspaces found"
	// record instead of rewinding away, so the terminal is never
	// blank between it and the readiness card that follows (#120).
	// Skip reporting is deferred until the card lands — a skip card
	// printed mid-spinner would interleave with the live render.
	//
	// A workspace that won't resolve (unmatched repos, no worktrees)
	// is excluded with its reason — not force-includable, since these
	// are correctness hazards rather than acknowledged data risks.
	var (
		matched int
		targets []cleanupTarget
		skips   []string
	)
	err = ui.RunCardThen("Resolving Workspaces", func() error {
		observed := observeWorkspaces(ctx, names, func(ctx context.Context, name string) workspaceState {
			return fetchWorkspaceIssueState(ctx, tracker, name)
		})

		matches, filterSkips := partitionWorkspaces(observed, query)
		matched = len(matches)
		skips = filterSkips

		projectRepos, err := resolveRepositories(nil)
		if err != nil {
			return err
		}
		mainPath := mainPathIndex(projectRepos)

		for _, ws := range matches {
			t, err := resolveCleanupTarget(ctx, ws.name, ws.issueKey, mainPath)
			if err != nil {
				skips = append(skips, fmt.Sprintf("%s: %v", ws.name, err))
				continue
			}
			targets = append(targets, t)
		}
		return nil
	}, func() *ui.Card {
		found := fmt.Sprintf("%d of %d workspaces found", len(targets), len(names))
		if len(targets) == len(names) {
			found = fmt.Sprintf("%d %s found", len(targets), pluralize(len(targets), "workspace", "workspaces"))
		}
		return ui.NewCard(ui.CardSuccess, "Resolving Workspaces").Value(found)
	})
	if err != nil {
		return err
	}
	for _, s := range skips {
		ui.Skip(s)
	}
	if matched == 0 {
		ui.Skip("no workspaces match the filter")
		return nil
	}
	if len(targets) == 0 {
		ui.Skip("no workspaces left to clean up")
		return nil
	}

	candidates, rewindReadiness, err := emitBulkCleanupReadiness(ctx, g, host, tracker, targets, force)
	if err != nil {
		return err
	}

	// Selection gate. Interactive: the readiness-annotated picker is
	// the selection — choosing a WARN (or --force-included BLOCK) row
	// is the acknowledgment, so no combined warning dialog follows.
	// Non-interactive: the pattern/filter was the selection; sweep
	// the candidate set with BLOCKs excluded (--force includes them)
	// and WARNs requiring --force as the stand-in for the
	// acknowledgment nobody is present to give.
	var included []bulkCleanupCandidate
	if isInteractive() {
		included, err = pickBulkCandidates(candidates, force, rewindReadiness)
		if err != nil {
			return err
		}
		if len(included) == 0 {
			ui.Skip("no workspaces selected")
			return nil
		}
	} else {
		included = includeBulkCandidates(candidates, force)
		if len(included) == 0 {
			ui.Skip("no workspaces passed cleanup readiness")
			return nil
		}
		if !force && anyCandidateAtOrAbove(included, findingWarn) {
			return fmt.Errorf("cleanup readiness: warnings present and no prompt available; re-run with --force to proceed")
		}
	}

	moved := &movedShell{}
	var actions []Action
	for _, c := range included {
		actualBranch := make(map[string]string, len(c.probes))
		for _, p := range c.probes {
			actualBranch[p.repo.Name] = p.branch
		}
		actions = append(actions, buildCleanupActions(ctx, g, c.target, actualBranch, force, moved)...)
	}

	err = runActions(cmd, ctx, actions)
	moved.emit()
	return err
}

// cleanupTarget bundles everything tearing one workspace down needs:
// the workspace-scoped repos (worktree paths), the main-checkout
// lookup the git destroys run against, and the on-disk workspace
// location.
type cleanupTarget struct {
	workspace string
	issueKey  string
	wsRoot    string
	wsPath    string
	repos     []Repository      // workspace-scoped; .Path is the worktree
	mainPath  map[string]string // repo name → main checkout path
}

// resolveCleanupTarget resolves one workspace into a cleanupTarget.
// mainPath is the project-config repo index (mainPathIndex), computed
// once by the caller since it is workspace-independent.
//
// Every destroy action runs git against the repo's main checkout. A
// workspace repo the project config no longer matches would look up
// to an empty path — and git with Dir="" operates on the caller's
// cwd, aiming `branch -D` / `push origin --delete` at whatever repo
// the user happens to be standing in. Refuse instead.
func resolveCleanupTarget(ctx context.Context, workspace, issueKey string, mainPath map[string]string) (cleanupTarget, error) {
	// Workspace-scoped repos carry the worktree path on
	// Repository.Path; the safety check uses that for its
	// dirty / branch-sync / HEAD probes.
	workspaceRepos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return cleanupTarget{}, err
	}

	var unmatched []string
	for _, r := range workspaceRepos {
		if mainPath[r.Name] == "" {
			unmatched = append(unmatched, r.Name)
		}
	}
	if len(unmatched) > 0 {
		return cleanupTarget{}, fmt.Errorf(
			"workspace repo(s) %s not matched by the project's repositories config; re-add them (or remove the worktrees manually) before cleanup",
			strings.Join(unmatched, ", "))
	}

	wsRoot := viper.GetString("workspace.root")
	if projectRoot := config.FindProjectRoot(); !filepath.IsAbs(wsRoot) && projectRoot != "" {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	return cleanupTarget{
		workspace: workspace,
		issueKey:  issueKey,
		wsRoot:    wsRoot,
		wsPath:    filepath.Join(wsRoot, workspace),
		repos:     workspaceRepos,
		mainPath:  mainPath,
	}, nil
}

// mainPathIndex indexes project-configured repos by name for the
// main-checkout lookups the destroy actions need.
func mainPathIndex(repos []Repository) map[string]string {
	m := make(map[string]string, len(repos))
	for _, r := range repos {
		m[r.Name] = r.Path
	}
	return m
}

// movedShell records the one chdir a cleanup run may perform when the
// process is standing inside a workspace it removes. Set by the
// workspace-directory action's Apply, surfaced as a "cd back" hint
// after the plan finalizes (actions run sequentially, so no locking).
type movedShell struct {
	from string
	to   string
}

// emit prints the cd-back hint when the shell was moved. Runs after
// runActions regardless of its error: a bulk sweep can move the shell
// for one workspace and fail on another, and the hint still applies.
// The move only happens inside the directory action, so a workspace
// whose own failure gates that action off strands the shell with no
// hint — the directory it stood in is gone, but nothing here can tell
// that apart from a run that never destroyed it (a cancelled plan,
// say) without plumbing per-worktree outcomes through.
func (m *movedShell) emit() {
	if m.from != "" {
		ui.Info("shell is in a removed directory (%s); cd to %s", m.from, m.to)
	}
}

// buildCleanupActions assembles the per-workspace action set: preview
// teardown first (the env goes down before any local destruction),
// per-repo worktree and branch destroys, then the workspace-directory
// removal. Every action carries the workspace name as its plan group,
// so in a bulk plan the directory removal — which asserts "this
// workspace fully cleaned" and is therefore gated — is withheld only
// behind failures in its own workspace, never a sibling's.
func buildCleanupActions(ctx context.Context, g vcs.VCS, target cleanupTarget, actualBranch map[string]string, force bool, moved *movedShell) []Action {
	var actions []Action
	workspace := target.workspace

	// Preview env teardown. Idempotent when no env is bound.
	//
	// A provider that won't build is reported, not skipped
	// silently. The workflow-dispatch provider declares no
	// required config so this never failed; one that needs a
	// base URL fails routinely when it isn't set, and cleanup
	// would then destroy the worktrees and leave the env
	// running with nothing said about it.
	//
	// A project that selects no provider is the one case with nothing
	// to say: it deploys no previews, so no environment can be
	// stranded, and the refusal it would otherwise report reads as an
	// instruction to configure a capability it declined.
	provider, providerErr := newPreviewProvider(workspace)
	switch {
	case errors.Is(providerErr, services.ErrProviderNotSelected):
		// Not configured — nothing to tear down and nothing to say.
	case providerErr != nil:
		ui.Skip(fmt.Sprintf("preview teardown: %v", providerErr))
	case provider != nil && target.issueKey != "":
		ready := &previewReadiness{provider: provider}
		action := cleanupPreviewAction(ctx, ready, provider, target.issueKey)
		action.Group = workspace
		actions = append(actions, action)
	}

	for i := range target.repos {
		r := target.repos[i]
		repoName := r.Name
		worktreePath := r.Path
		repoPath := target.mainPath[repoName]

		actions = append(actions, Action{
			Op:     ui.PlanDestroy,
			Action: "worktree",
			Type:   "repo",
			Name:   repoName,
			Group:  workspace,
			Assess: func(_ context.Context) (ActionState, string, error) {
				if _, err := os.Stat(worktreePath); err != nil {
					return ActionCompleted, workspace, nil
				}
				return ActionNeeded, workspace, nil
			},
			Apply: func(ctx context.Context) error {
				return g.RemoveWorktree(ctx, repoPath, worktreePath, force)
			},
		})
	}

	for i := range target.repos {
		r := target.repos[i]
		repoName := r.Name
		repoPath := target.mainPath[repoName]
		branch := actualBranch[repoName]
		if branch == "" {
			branch = workspace
		}

		actions = append(actions, Action{
			Op:     ui.PlanDestroy,
			Action: "branch",
			Type:   "repo",
			Name:   repoName,
			Group:  workspace,
			Assess: func(ctx context.Context) (ActionState, string, error) {
				exists, err := g.BranchExists(ctx, repoPath, branch)
				if err != nil {
					return 0, "", err
				}
				detail := fmt.Sprintf("%s (local + remote)", branch)
				if !exists {
					return ActionCompleted, detail, nil
				}
				return ActionNeeded, detail, nil
			},
			Apply: func(ctx context.Context) error {
				return g.DeleteBranch(ctx, repoPath, branch)
			},
		})
	}

	// Workspace directory removal — a plan row of its own rather
	// than a post-apply step, so the approval discloses it and a
	// partially-failed workspace keeps its directory as the visible
	// sign the run didn't finish (the gate skips this action behind
	// any failure in the same group). Stray files inside wsPath are
	// gated by the readiness check; by the time this applies they've
	// been explicitly acknowledged (--force) and are destroyed.
	//
	// Whether the process is standing inside this workspace — and
	// where the project root is — is detected NOW, not at apply time:
	// the worktree rows apply first, and once they delete the CWD out
	// from under the process, neither os.Getwd nor the CWD-walking
	// FindProjectRoot can answer (Linux getcwd errors outright on a
	// deleted directory). The chdir itself still waits for the apply;
	// an unapproved plan must not move the shell.
	var escapeFrom string
	escapeTo := config.FindProjectRoot()
	if detected, _ := detectWorkspaceFromCWD(); detected == workspace {
		escapeFrom, _ = os.Getwd()
	}
	wsPath := target.wsPath
	wsRoot := target.wsRoot
	actions = append(actions, Action{
		Op:                   ui.PlanDestroy,
		Action:               "directory",
		Type:                 "workspace",
		Name:                 workspace,
		Group:                workspace,
		RequiresPriorSuccess: true,
		Assess: func(_ context.Context) (ActionState, string, error) {
			if _, err := os.Stat(wsPath); err != nil {
				return ActionCompleted, "", nil
			}
			return ActionNeeded, "", nil
		},
		Apply: func(_ context.Context) error {
			return removeWorkspaceDir(wsPath, wsRoot, escapeFrom, escapeTo, moved)
		},
	})

	return actions
}

// removeWorkspaceDir removes the workspace directory and any junk-only
// (or empty) parents up to the workspace root — e.g. "epic/" once the
// last EX-* workspace under it is cleaned. escapeFrom, when non-empty,
// is the CWD the process was standing in inside this workspace, and
// escapeTo the project root — both captured before the worktrees were
// removed: the process moves to the project root first (the same
// escape hatch workspace delete uses) and the move is recorded for
// the post-plan hint.
func removeWorkspaceDir(wsPath, wsRoot, escapeFrom, escapeTo string, moved *movedShell) error {
	if escapeFrom != "" && escapeTo != "" {
		if err := os.Chdir(escapeTo); err != nil {
			return fmt.Errorf("moving to project root: %w", err)
		}
		moved.from, moved.to = escapeFrom, escapeTo
	}

	if err := os.RemoveAll(wsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing workspace directory: %w", err)
	}
	for dir := filepath.Dir(wsPath); dir != wsRoot && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil || fsutil.HasMeaningfulEntries(entries) {
			break
		}
		// Junk-only (or empty) parent: RemoveAll so a lingering
		// .DS_Store doesn't leave the directory behind.
		_ = os.RemoveAll(dir)
	}
	return nil
}

// cleanupPreviewAction builds the preview-env teardown row in
// cleanup's action plan. Assess checks whether an env is bound; no
// env → ActionSkipped, omitting the row entirely — a "no change"
// row would assert "we will not touch this subject", which
// presupposes a subject exists. Apply tears down via the provider —
// idempotent on the adapter side, so a stale registry entry doesn't
// cause failure.
//
// The provider is asked whether it can tear down at all, the same way
// `bosun preview` asks. This is the second of the two places that call
// Destroy, and until it asked, "the provider answers for itself
// whether each half of its lifecycle is wired" held in one of them:
// an unwired provider got an operative row here that failed at apply,
// after cleanup had already destroyed the worktrees.
//
// Readiness is settled here rather than inside Assess because the
// reason travels on ui.Skip, and Assess runs inside the assessment
// spinner. The cost is that a run with no env bound still hears why a
// teardown it did not need could not have happened; the alternative is
// a reason that arrives inside a spinner or not at all.
func cleanupPreviewAction(ctx context.Context, ready *previewReadiness, provider preview.Provider, issueKey string) Action {
	reason, err := ready.reason(ctx, preview.OpDestroy)
	switch {
	case err != nil:
		return failedAction(ui.PlanDestroy, "teardown", issueKey, err)
	case reason != "":
		return noopAction("teardown", issueKey, reason)
	}

	var envName string
	return Action{
		Op:     ui.PlanDestroy,
		Action: "teardown",
		Type:   "env",
		Name:   issueKey,
		Assess: func(ctx context.Context) (ActionState, string, error) {
			env, err := provider.Get(ctx, issueKey)
			if err != nil {
				if errors.Is(err, preview.ErrNoEnvironment) {
					// Definitively no env — nothing to tear down and
					// nothing to say about it.
					return ActionSkipped, "", nil
				}
				// Probe failure (network, indeterminate) — still
				// attempt teardown so a registry entry doesn't strand.
				// envName keeps the API value (possibly empty for the
				// provider to resolve); only the plan detail shows the
				// "(unknown)" placeholder.
				envName = env.Name
				detail := envName
				if detail == "" {
					detail = "(unknown)"
				}
				return ActionNeeded, detail, nil
			}
			envName = env.Name
			return ActionNeeded, envName, nil
		},
		Apply: func(ctx context.Context) error {
			return provider.Destroy(ctx, issueKey, envName)
		},
	}
}
