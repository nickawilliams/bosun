package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// releaseTarget is one workspace repo resolved for prerelease: its
// remote identity, the branch to tag, the latest tag and derived next
// version, and whether the user selected it to cut a release.
// currentTag/nextVersion/tagErr are filled by selectReleaseTargets; the
// rest by the identity-resolution loop.
type releaseTarget struct {
	repo     Repository
	branch   string
	owner    string
	repoName string

	currentTag  string // latest tag ("" = no tags yet)
	nextVersion string // derived next version
	tagErr      error  // identity or tag-fetch failure, surfaced as a ✗ plan row
	include     bool   // selected to cut a release
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
			readiness, _, anyUnpushed, err := gatherRepoReadiness(ctx, g, repositories)
			if err != nil {
				return err
			}
			if err := emitWorkspaceReadiness(ctx, g, readiness, anyUnpushed); err != nil {
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
				repo    string
				release code.Release
				version string
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
									releaseResults = append(releaseResults, releaseResult{
										repo: rt.repo.Name, release: code.Release{Tag: rt.currentTag}, version: rt.currentTag,
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
								repo: rt.repo.Name, release: rel, version: rt.nextVersion,
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
							items[i] = notify.Item{
								Label:  r.repo,
								URL:    r.release.URL,
								Detail: r.version,
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

// selectReleaseTargets fetches each repo's latest tag under a spinner
// sequence, derives the next version, then — when interactive — offers a
// multi-select of the eligible repos so the user can choose which get a
// release cut. Repos already at the target version (or with errors)
// aren't offered; they appear in the result card and the plan. Mirrors
// review's selectReviewTargets so the Observe → Select → record arc reads
// the same across commands. Mutates targets in place: sets
// currentTag/nextVersion/tagErr on every repo and include on the eligible
// ones.
func selectReleaseTargets(ctx context.Context, cmd *cobra.Command, host code.Host, bump string, targets []releaseTarget) error {
	if host == nil || len(targets) == 0 {
		return nil
	}

	// --- Phase 1: latest-tag lookup, one program over all repos ---

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

	// applyDefaults seeds include from the eligibility policy — used
	// whenever no form shows (non-interactive, -y, or nothing eligible).
	// --repository has already narrowed the repo set upstream, so the
	// default is simply "release every eligible repo in scope."
	applyDefaults := func() {
		for i := range targets {
			targets[i].include = targets[i].eligible()
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

	// --- Phase 2: selection form over eligible repos ---

	var idxs []int
	for i := range targets {
		if targets[i].eligible() {
			idxs = append(idxs, i)
		}
	}
	sort.SliceStable(idxs, func(a, b int) bool {
		return targets[idxs[a]].repo.Name < targets[idxs[b]].repo.Name
	})

	// Bold the repo segment (raw SGR 1/22, not lipgloss, so huh's own
	// selection styling for the rest of the line survives). The version
	// transition follows as the row's status.
	opts := make([]huh.Option[string], len(idxs))
	for j, i := range idxs {
		rt := &targets[i]
		label := "\x1b[1m" + rt.repo.Name + "\x1b[22m"
		if note := rt.versionNote(); note != "" {
			label += " · " + note
		}
		opts[j] = huh.NewOption(label, strconv.Itoa(i)).Selected(rt.preselect())
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

	pickedSet := make(map[int]bool, len(picked))
	for _, p := range picked {
		if i, err := strconv.Atoi(p); err == nil {
			pickedSet[i] = true
		}
	}
	for _, i := range idxs {
		targets[i].include = pickedSet[i]
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
			rows = append(rows, row{name, glyphOK, on(name, rt.versionNote())})
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
