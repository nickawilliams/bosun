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
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// releaseTarget is one workspace repo resolved for prerelease: its
// remote identity, the branch to tag, the latest tag and derived next
// version, and the per-service selection that drives the notification.
//
// The identity loop fills repo/branch/owner/repoName/tagErr;
// selectReleaseTargets fills the rest: currentTag, nextVersion, and the
// service-detection trio (services, affectedServices, subjects).
//
// The form is service-granular even when the GitHub release is per-repo:
// `include` is derived from "any subject selected for this repo" so a
// user can opt out of an entire repo by unchecking every one of its
// services. `subjects` is the final list that lands in the notification
// label.
type releaseTarget struct {
	repo     Repository
	branch   string
	owner    string
	repoName string

	currentTag  string // latest tag ("" = no tags yet)
	nextVersion string // derived next version
	tagErr      error  // identity or tag-fetch failure, surfaced as a ✗ plan row

	// services is the full configured service list for the repo
	// (resolveRepoServiceNames). Empty when the repo has no services
	// config — the fallback row in that case uses the repo name itself
	// as the synthetic "service."
	services []string
	// affectedServices is the detection result narrowed by the
	// per-service path-map. nil means narrowing wasn't possible (no
	// path-map / first release / diff failure); the form pre-checks all
	// services in that case. An empty (non-nil) slice means narrowing
	// ran and found no affected services — pre-check none.
	affectedServices []string
	// subjects is the user's final selection (or applyDefaults' seed).
	// What we render in the notification label and the result card.
	subjects []string

	include bool // selected to release; derived from len(subjects) > 0
}

// eligible reports whether rt is a candidate for a new release: its
// lookup succeeded and the derived next version differs from the latest
// tag. Repos already at the target version (or with errors) are not
// offered in the selection form.
func (rt *releaseTarget) eligible() bool {
	return rt.tagErr == nil && rt.nextVersion != "" && rt.nextVersion != rt.currentTag
}

// preselect reports whether an eligible repo should be pre-checked in
// the selection form (and included by default when no form shows). Every
// eligible repo pre-checks — there's no opt-in edge case the way review's
// merged-PR case is.
func (rt *releaseTarget) preselect() bool { return rt.eligible() }

// versionNote returns the per-repo status shown in the selection form and
// result card: the latest released tag, or "(none)" when the repo has no
// tags yet. Pure status — the version transition (and the reason for any
// no-op) lives in the plan, not the card.
func (rt *releaseTarget) versionNote() string {
	if rt.currentTag != "" {
		return rt.currentTag
	}
	return "(none)"
}

func newPrereleaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prerelease",
		Short: "Prepare release artifacts",
		Annotations: map[string]string{
			headerAnnotationTitle: "prepare release",
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
			bump, _ := cmd.Flags().GetString("bump")

			// --- Resolve ---

			_ = emitLifecyclePreamble(ctx, issue)

			filterRepositories, _ := cmd.Flags().GetStringSlice("repository")
			repositories, err := resolveActiveRepositories(ctx, cc.Workspace, filterRepositories)
			if err != nil {
				return err
			}

			host, hostErr := newCodeHost()
			if hostErr != nil {
				ui.Skip(fmt.Sprintf("code host: %v", hostErr))
			}

			// --- Pre-flight: Workspace Readiness ---
			//
			// Shared readiness section (same as review/preview/release):
			// one card per repo with the push offer + dirty gate folded
			// in. Pushing matters here too — CreateRelease tags a
			// commitish on the remote, so a never-pushed branch has no ref
			// to tag (it drops out of the release set) and a
			// pushed-but-behind branch would otherwise tag stale commits.
			g := git.New()
			readiness, _, err := emitWorkspaceReadiness(ctx, g, repositories)
			if err != nil {
				return err // ErrCancelled (dirty gate) propagates as a clean abort
			}

			// Build release targets from the repos that have a remote
			// branch to tag. A never-pushed branch has no remote ref for
			// CreateRelease, so it drops out with a note rather than
			// failing mid-apply. Identity-resolution failures ride on
			// tagErr so the repo still appears as a ✗ row.
			targets := make([]releaseTarget, 0, len(readiness))
			for i := range readiness {
				rr := &readiness[i]
				if !rr.hasRemoteBranch() {
					ui.SkipValue(ui.PreserveCase(rr.repo.Name), "not pushed — no remote branch to release")
					continue
				}
				rt := releaseTarget{repo: rr.repo, branch: rr.branch}
				if identity, err := gh.ParseRemote(ctx, rr.repo.Path); err != nil {
					rt.tagErr = err
				} else {
					rt.owner = identity.Owner
					rt.repoName = identity.Name
				}
				targets = append(targets, rt)
			}

			// Observe → Select → record which repos get a release.
			if err := selectReleaseTargets(ctx, cmd, host, bump, targets); err != nil {
				return err
			}

			// --- Plan + Apply ---

			var actions []Action

			type releaseResult struct {
				repo     string
				services []string // configured services for the repo
				subjects []string // user-selected services; formatSubjects collapses to repo when full coverage
				release  code.Release
				version  string
			}
			var releaseResults []releaseResult

			if host != nil {
				for i := range targets {
					rt := &targets[i]
					actions = append(actions, Action{
						Op:     ui.PlanCreate,
						Action: "release",
						Type:   "repo",
						Name:   rt.repo.Name,
						Assess: func(_ context.Context) (ActionState, string, error) {
							if rt.tagErr != nil {
								return 0, "", rt.tagErr
							}
							if !rt.include {
								// Already at a release version — capture it so
								// notifications still list the repo, and render
								// an unchanged row (the = glyph already reads as
								// "current"). Deselected-eligible repos fall
								// through to a "not selected" no-op (not
								// announced), which distinguishes the two in the
								// plan.
								if !rt.eligible() && rt.currentTag != "" {
									// Already-current row in the notification — use
									// the configured services + the prior subjects
									// so the label format stays consistent with the
									// active-release case (collapses to repo name
									// when all services were "shipped" previously).
									releaseResults = append(releaseResults, releaseResult{
										repo:     rt.repo.Name,
										services: rt.services,
										subjects: rt.subjects,
										release:  code.Release{Tag: rt.currentTag},
										version:  rt.currentTag,
									})
									return ActionCompleted, rt.currentTag, nil
								}
								return ActionCompleted, "not selected", nil
							}
							from := rt.currentTag
							if from == "" {
								from = "(none)"
							}
							return ActionNeeded, fmt.Sprintf("%s → %s", from, rt.nextVersion), nil
						},
						Apply: func(ctx context.Context) error {
							// Subjects were already resolved during the spinner
							// phase of selectReleaseTargets and finalized by the
							// form (or applyDefaults in the non-interactive
							// path). Apply just reads them through.
							rel, err := host.CreateRelease(ctx, code.CreateReleaseRequest{
								Owner:         rt.owner,
								Repository:    rt.repoName,
								Tag:           rt.nextVersion,
								Target:        rt.branch,
								Name:          rt.nextVersion,
								GenerateNotes: true,
							})
							if err != nil {
								return err
							}
							releaseResults = append(releaseResults, releaseResult{
								repo:     rt.repo.Name,
								services: rt.services,
								subjects: rt.subjects,
								release:  rel,
								version:  rt.nextVersion,
							})
							return nil
						},
					})
				}
			}

			tracker, _ := newIssueTracker()
			if sa, ok := statusAction(tracker, issue, "", "ready_for_release"); ok {
				actions = append(actions, sa)
			}

			releaseChannel := viper.GetString("notification.channel_release")
			releaseNotifier, releaseNotifierErr := newNotifier()
			if releaseNotifierErr == nil {
				defer releaseNotifier.Close()
			}
			if releaseChannel != "" && releaseNotifierErr == nil {
				releaseNotifyOp := ui.PlanCreate
				actions = append(actions, Action{
					Op:     ui.PlanCreate,
					OpRef:  &releaseNotifyOp,
					Action: "notify",
					Type:   "channel",
					Name:   releaseChannel,
					Assess: func(ctx context.Context) (ActionState, string, error) {
						ref, _ := releaseNotifier.FindThread(ctx, releaseChannel, issue)
						if ref.Timestamp != "" {
							releaseNotifyOp = ui.PlanModify
							return ActionNeeded, "update notification", nil
						}
						return ActionNeeded, "new notification", nil
					},
					Apply: func(ctx context.Context) error {
						if len(releaseResults) == 0 {
							return nil
						}
						sort.Slice(releaseResults, func(i, j int) bool {
							return releaseResults[i].repo < releaseResults[j].repo
						})
						items := make([]notify.Item, len(releaseResults))
						for i, r := range releaseResults {
							// formatSubjects collapses to repo name when the
							// user kept (or seeded) all services, so the team's
							// "we shipped everything in this repo" announcement
							// renders as `` `host-ui`: <url> `` rather than a
							// long comma list. The "`, `" separator lives
							// between adjacent service names inside the
							// template's outer backticks, producing
							//     going out `svc-a`, `svc-b`: <url>
							// for the partial-selection case.
							items[i] = notify.Item{
								Label:  formatSubjects(r.repo, r.services, r.subjects, "`, `"),
								URL:    r.release.URL,
								Detail: r.version,
								Body:   r.release.Body, // host-generated release notes
							}
						}
						_, err := releaseNotifier.Notify(ctx, notify.Message{
							Channel:  releaseChannel,
							IssueKey: issue,
							Items:    items,
							Content: buildNotifyContent("release", notifyTemplateData{
								IssueKey: issue,
								Items:    items,
							}),
						})
						return err
					},
				})
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
	cmd.Flags().String("bump", "patch", "version bump level (patch|minor|major)")
	cmd.Flags().StringSlice("repository", nil, "filter repositories to operate on")
	return cmd
}

// selectReleaseTargets resolves each repo's release state under a
// spinner sequence — latest tag, next version, configured services, and
// detected-affected services — then (when interactive) offers a flat
// per-service multi-select so the user can choose which services get
// announced in the release notification. Repos already at the target
// version (or with errors) aren't offered in the form; they appear in
// the result card and the plan. Mirrors review's selectReviewTargets so
// the Observe → Select → record arc reads the same across commands.
//
// Mutates targets in place: sets currentTag/nextVersion/tagErr/services/
// affectedServices on every eligible repo; subjects + include are set by
// the form (or applyDefaults).
func selectReleaseTargets(ctx context.Context, cmd *cobra.Command, host code.Host, bump string, targets []releaseTarget) error {
	if host == nil || len(targets) == 0 {
		return nil
	}

	// --- Phase 1: per-repo resolution + service detection ---
	//
	// Service detection runs alongside the tag fetch because both happen
	// per repo, both want spinner feedback, and the detection's diff
	// base IS the just-fetched currentTag.
	g := git.New()
	statusMuted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	steps := make([]ui.CardStep, 0, len(targets))
	for i := range targets {
		rt := &targets[i]
		spin := ui.NewCard(ui.CardRunning, "releases").
			Raw(statusMuted.Render("Checking ") +
				ui.Keyword(rt.repo.Name) +
				statusMuted.Render("..."))
		steps = append(steps, ui.CardStep{
			Card: spin,
			Run: func() error {
				if rt.tagErr != nil {
					return nil // identity already failed; surfaced as a ✗ row
				}
				tag, err := host.GetLatestTag(ctx, rt.owner, rt.repoName)
				if err != nil {
					rt.tagErr = err
					return nil
				}
				rt.currentTag = tag
				next, err := code.DeriveNextVersion(tag, bump)
				if err != nil {
					rt.tagErr = err
					return nil
				}
				rt.nextVersion = next

				// Service detection: configured set + path-map narrowing.
				rt.services = resolveRepoServiceNames(rt.repo.Name)
				if len(rt.services) > 1 {
					pathMap := resolveServicePaths(rt.repo.Name)
					rt.affectedServices = detectAffectedServices(ctx, g, rt.repo.Path, rt.currentTag, rt.services, pathMap)
				}
				return nil
			},
		})
	}

	formGate := func() bool {
		if !isInteractive() || isAutoApprove(cmd) {
			return false
		}
		for i := range targets {
			if targets[i].eligible() {
				return true
			}
		}
		return false
	}

	// applyDefaults seeds each eligible repo's subjects + include from
	// the detection result — used whenever no form shows (non-
	// interactive, -y, or nothing eligible). Matches the form's
	// pre-check policy so the rendered notification is identical
	// whether or not the user saw the form.
	applyDefaults := func() {
		for i := range targets {
			rt := &targets[i]
			if !rt.eligible() {
				continue
			}
			rt.subjects = defaultSubjectsFor(rt)
			rt.include = len(rt.subjects) > 0
		}
	}

	rewind, err := ui.RunCardSteps(steps, func() *ui.Card {
		if formGate() {
			return ui.NewCard(ui.CardInput, "releases").Tight()
		}
		applyDefaults()
		return buildReleaseTargetsCard(targets)
	})
	if err != nil {
		return err
	}

	if !formGate() {
		applyDefaults()
		return nil
	}

	// --- Phase 2: selection form, one row per service ---
	//
	// Rows are flat (`repo · service`) sorted by (repo, service). Repos
	// without configured services contribute a single fallback row with
	// the repo name as the synthetic "service." Each option's key
	// encodes the target index and service index ("i.j") so the form
	// result maps back unambiguously.

	type optRow struct {
		repoIdx    int
		serviceIdx int   // -1 = fallback "no services configured" row
		preselect  bool
	}
	var rows []optRow
	for i := range targets {
		rt := &targets[i]
		if !rt.eligible() {
			continue
		}
		// No services configured → single fallback row carrying the
		// repo name as a synthetic service. applyDefaults / form logic
		// reads this back as "subjects = [repoName]".
		if len(rt.services) == 0 {
			rows = append(rows, optRow{repoIdx: i, serviceIdx: -1, preselect: true})
			continue
		}
		preCheck := preCheckPolicy(rt)
		for j, svc := range rt.services {
			_ = svc
			rows = append(rows, optRow{repoIdx: i, serviceIdx: j, preselect: preCheck[j]})
		}
	}
	sort.SliceStable(rows, func(a, b int) bool {
		ra, rb := &targets[rows[a].repoIdx], &targets[rows[b].repoIdx]
		if ra.repo.Name != rb.repo.Name {
			return ra.repo.Name < rb.repo.Name
		}
		// Stable within a repo: fallback row first (only one), else
		// services in their config-declared order via serviceIdx.
		return rows[a].serviceIdx < rows[b].serviceIdx
	})

	opts := make([]huh.Option[string], len(rows))
	for k, row := range rows {
		rt := &targets[row.repoIdx]
		// Bold the repo segment (raw SGR 1/22 so huh's selection
		// styling for the rest of the row survives — lipgloss would
		// close with a full reset). The current version intentionally
		// isn't shown here — the form asks "which services?", and the
		// version transition belongs on the plan card where the user
		// is being asked to approve the actual change.
		label := "\x1b[1m" + rt.repo.Name + "\x1b[22m"
		if row.serviceIdx >= 0 {
			label += " · " + rt.services[row.serviceIdx]
		}
		key := fmt.Sprintf("%d.%d", row.repoIdx, row.serviceIdx)
		opts[k] = huh.NewOption(label, key).Selected(row.preselect)
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
		return err
	}

	// Apply the form result: clear subjects on every eligible repo,
	// then re-populate from the picked keys. include falls out as
	// "any subject selected for this repo."
	for i := range targets {
		if targets[i].eligible() {
			targets[i].subjects = nil
			targets[i].include = false
		}
	}
	for _, p := range picked {
		ri, si, ok := parseSubjectKey(p)
		if !ok || ri < 0 || ri >= len(targets) {
			continue
		}
		rt := &targets[ri]
		if si < 0 {
			rt.subjects = append(rt.subjects, rt.repo.Name)
		} else if si < len(rt.services) {
			rt.subjects = append(rt.subjects, rt.services[si])
		}
		rt.include = true
	}

	// Erase the form header and drop the result card in its place.
	rewind()
	buildReleaseTargetsCard(targets).Print()
	return nil
}

// buildReleaseTargetsCard composes the static "Releases" card: one row
// per repo showing the repo and its latest tag (`repo · v1.2.3`, or
// `· (none)` when unreleased; the error text for a failed row). Mirrors
// review's buildReviewTargetsCard: rows are pure status, the glyph
// carries whether the repo is acted on (✓) or skipped (○), and the plan
// is the source of truth for the version transition and for why each
// no-op is a no-op. The card glyph aggregates worst-first (fail > skipped
// > success).
func buildReleaseTargetsCard(targets []releaseTarget) *ui.Card {
	repoStyle := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	glyphOK := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render("✓")
	glyphOff := muted.Render("○")
	glyphFail := lipgloss.NewStyle().Foreground(ui.Palette.Error).Render("✗")

	type row struct {
		repo, glyph, content string
	}
	var rows []row
	var nOK, nSkip, nFail int

	// on: included rows keep the repo in primary color; off: skipped rows
	// recede entirely. A blank note renders the repo name alone.
	on := func(name, note string) string {
		if note == "" {
			return repoStyle.Render(name)
		}
		return repoStyle.Render(name) + muted.Render(" · "+note)
	}
	off := func(name, note string) string {
		if note == "" {
			return muted.Render(name)
		}
		return muted.Render(name + " · " + note)
	}

	for i := range targets {
		rt := &targets[i]
		name := rt.repo.Name
		switch {
		case rt.tagErr != nil:
			nFail++
			rows = append(rows, row{name, glyphFail, on(name, rt.tagErr.Error())})
		case rt.include:
			nOK++
			// Append the user-selected subjects in parens when partial;
			// full-coverage selections render as just the version (same
			// shape as today). formatSubjects returns the repo name for
			// the full case, which we strip back out — we don't want to
			// echo the repo name we already printed at the row start.
			note := rt.versionNote()
			if sel := formatSubjects(rt.repo.Name, rt.services, rt.subjects, ", "); sel != rt.repo.Name {
				note += " (" + sel + ")"
			}
			rows = append(rows, row{name, glyphOK, on(name, note)})
		default:
			nSkip++
			rows = append(rows, row{name, glyphOff, off(name, rt.versionNote())})
		}
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].repo < rows[j].repo })

	state := ui.CardSuccess
	switch {
	case nFail > 0:
		state = ui.CardFailed
	case nSkip > 0 && nOK == 0:
		state = ui.CardSkipped
	}

	card := ui.NewCard(state, "releases")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
	}
	return card
}

// detectAffectedServices narrows a repo's configured service list down
// to the services whose files changed between currentTag and HEAD. The
// detection mirrors preview's per-service path-map filtering, but diffs
// against the previous tag rather than the default branch because that's
// the slice of history this release actually carries.
//
// Returns nil (not empty) when narrowing isn't possible — the form
// treats that as "we don't know, pre-check all services." Returns an
// empty (non-nil) slice only when the diff genuinely found no matching
// service.
//
// Cases that return nil ("can't narrow"):
//   - First release (no previous tag to diff against).
//   - No per-service path-map configured (list form of `services.<repo>`).
//   - Diff command failure.
//   - currentTag and HEAD point at the same commit (empty diff).
//
// Single-service repos and repos with no services configured don't reach
// this function — the caller short-circuits both.
func detectAffectedServices(ctx context.Context, g vcs.VCS, repoPath string, currentTag string, services []string, pathMap map[string][]string) []string {
	if currentTag == "" || pathMap == nil {
		return nil
	}
	changed, err := g.ChangedFiles(ctx, repoPath, currentTag)
	if err != nil || len(changed) == 0 {
		return nil
	}
	result := matchServicePaths("", services, changed, pathMap)
	sort.Strings(result.Services)
	return result.Services
}

// formatSubjects renders a service-list display string for a release.
// Collapses to the repo name when subjects covers ALL configured
// services (or when no services are configured), because a "we shipped
// everything" announcement reads cleaner as the repo name than as a
// long comma list. Otherwise joins subjects with sep so callers can
// pick a separator that matches their surrounding context (`` `, ` ``
// inside backticks for the Slack template, `, ` plain for the result
// card).
//
// services may be empty (no services configured) — in that case any
// non-empty subjects collapse to the repo name, matching the fallback
// row behavior.
func formatSubjects(repoName string, services, subjects []string, sep string) string {
	if len(subjects) == 0 {
		return repoName
	}
	if len(services) == 0 || len(subjects) == len(services) {
		return repoName
	}
	return strings.Join(subjects, sep)
}

// defaultSubjectsFor returns the initial subject list for an eligible
// repo before the user has a chance to override — used by both the
// non-interactive `applyDefaults` path and (transitively) the form's
// pre-check policy. Behavior matches the form's pre-checks so the
// interactive and non-interactive paths render the same notification
// when the user keeps the defaults.
//
// Rules (in order):
//   - No services configured → [repo name] (the synthetic fallback).
//   - Single service         → [that service].
//   - Detection narrowed     → the detected subset.
//   - Detection couldn't narrow → all services (errs toward inclusion).
//
// formatSubjects collapses cases where the result equals the full
// services list back to the repo name in the rendered output, so the
// "we don't know which" case still announces as the repo name unless
// the user prunes.
func defaultSubjectsFor(rt *releaseTarget) []string {
	if len(rt.services) == 0 {
		return []string{rt.repo.Name}
	}
	if len(rt.services) == 1 {
		return []string{rt.services[0]}
	}
	if rt.affectedServices != nil {
		out := make([]string, len(rt.affectedServices))
		copy(out, rt.affectedServices)
		return out
	}
	// Couldn't narrow — include all services.
	out := make([]string, len(rt.services))
	copy(out, rt.services)
	return out
}

// preCheckPolicy returns a parallel-to-rt.services bool slice indicating
// which services should be pre-checked in the form. Mirrors
// defaultSubjectsFor's set so the form's initial selection produces the
// same subjects an untouched default would.
func preCheckPolicy(rt *releaseTarget) []bool {
	checked := make([]bool, len(rt.services))
	defaults := defaultSubjectsFor(rt)
	want := make(map[string]struct{}, len(defaults))
	for _, s := range defaults {
		want[s] = struct{}{}
	}
	for i, s := range rt.services {
		if _, ok := want[s]; ok {
			checked[i] = true
		}
	}
	return checked
}

// parseSubjectKey decodes an "i.j" form-option key back into (repoIdx,
// serviceIdx). Returns ok=false on malformed input. serviceIdx may be -1
// to signal the fallback "no services configured" row.
func parseSubjectKey(key string) (repoIdx, serviceIdx int, ok bool) {
	dot := strings.IndexByte(key, '.')
	if dot < 0 {
		return 0, 0, false
	}
	ri, err := strconv.Atoi(key[:dot])
	if err != nil {
		return 0, 0, false
	}
	si, err := strconv.Atoi(key[dot+1:])
	if err != nil {
		return 0, 0, false
	}
	return ri, si, true
}
