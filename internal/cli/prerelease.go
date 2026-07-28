package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
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

	// containingRelease is set when our workspace's HEAD is already
	// reachable from an existing release tag (sweep-up case: another
	// user cut a release whose range included our work). Non-nil →
	// not eligible for a new release; the existing release stands in.
	containingRelease *code.Release

	// extraPRs lists PRs merged into the default branch in the
	// release range that aren't part of the workspace's own work.
	// Surfaced in the result card and notification so the user sees
	// "this release also ships PRs from other contributors."
	extraPRs []code.PullRequest

	// workspacePRNumber is the workspace's own PR for this repo, when
	// one is found via GetPRForBranch. Used to exclude it from
	// extraPRs and to label the workspace-branch context. Zero when
	// no PR exists (e.g. never pushed) — the extras list then
	// includes every merged PR in the range.
	workspacePRNumber int

	// workspacePRState is that PR's state ("open" | "draft" |
	// "closed" | "merged"), when a PR exists. Feeds the release gate:
	// only a merged PR puts the workspace's work on the default branch.
	workspacePRState string
	// workspacePRMergeableState and workspacePRReview are the PR's
	// mergeability signals (GitHub mergeable_state + review decision),
	// used to explain *why* an unmerged PR blocks the release
	// ("changes requested" / "checks failing" / "awaiting review" …).
	workspacePRMergeableState string
	workspacePRReview         string

	// gate is the per-repo release decision (allow / block / skip),
	// computed by resolveReleaseGate from the PR state plus an
	// on-default-branch ancestry check. gateReason is the human note
	// rendered on blocked/skipped rows.
	gate       releaseGate
	gateReason string

	// defaultBranch is the repo's default branch name (e.g. "main").
	// Resolved once during gather and used as CreateRelease's Target
	// (the tag always lands on default-branch HEAD), as the base for
	// the gate's ancestry check, and as the extras range endpoint.
	defaultBranch string
}

// releaseGate is the per-repo release decision.
type releaseGate int

const (
	// gateUnknown is the zero value — the gate hasn't been computed
	// (or couldn't be). Treated as not-allowed by eligible().
	gateUnknown releaseGate = iota
	gateAllow                // work is on the default branch — release it
	gateBlock                // work is NOT on default — refuse, with a reason
	gateSkip                 // nothing of ours to release — no-op, with a reason
)

// classifyReleaseGate decides whether a repo may be released, from the
// workspace PR (number/state; number 0 = no PR) and whether the branch
// is already contained in the default branch. Pure — the git ancestry
// check that produces branchOnDefault runs in the caller.
//
// The invariant: a release tags default-branch HEAD, so it must contain
// the workspace's work. A merged PR guarantees that — and, since GitHub
// only squash-merges through a PR, it sidesteps the "squashed tip isn't
// an ancestor of main" trap that would fool a raw ancestry check. Any
// unmerged PR means the work isn't on the default branch yet. With no PR
// at all, the ancestry check splits "branch adds nothing beyond default"
// (nothing to release → skip) from "unmerged commits, no PR" (block —
// open a PR and merge first).
func classifyReleaseGate(prNumber int, prState string, branchOnDefault bool) (releaseGate, string) {
	if prNumber > 0 {
		if prState == "merged" {
			return gateAllow, ""
		}
		return gateBlock, fmt.Sprintf("PR #%d %s — not merged", prNumber, prState)
	}
	if branchOnDefault {
		return gateSkip, "no changes to release from this workspace"
	}
	return gateBlock, "no PR — work not on the default branch"
}

// applyEmptyReleaseGuard downgrades an allowed release to a skip when
// the default branch is already fully contained in the latest tag
// (shipped) — there's nothing new to release. Pure; the caller supplies
// `shipped` from an IsMergedInto(origin/<default>, currentTag) check.
// Only an allow is ever downgraded; blocks/skips pass through unchanged.
func applyEmptyReleaseGuard(gate releaseGate, reason, currentTag string, shipped bool) (releaseGate, string) {
	if gate == gateAllow && shipped {
		return gateSkip, "already released — nothing new since " + currentTag
	}
	return gate, reason
}

// canMergePR reports whether a PR's mergeable_state means it can be
// merged right now — no conflicts, and required checks/reviews satisfied.
func canMergePR(mergeableState string) bool {
	return mergeableState == "clean" || mergeableState == "has_hooks"
}

// prMergeBlockReason turns an unmergeable open PR's state into a short
// human reason, so a blocked repo says *why* rather than a generic
// "not merged". Derived from mergeable_state + review decision (no extra
// checks fetch), mirroring status_render's vocabulary.
func prMergeBlockReason(mergeableState, review string) string {
	switch mergeableState {
	case "dirty":
		return "conflicts"
	case "behind":
		return "behind base"
	case "unstable":
		return "checks failing"
	case "draft":
		return "draft"
	case "blocked":
		switch review {
		case "changes_requested":
			return "changes requested"
		case "approved":
			return "checks required"
		default: // "awaiting" | ""
			return "awaiting review"
		}
	case "unknown", "":
		return "mergeability unknown"
	}
	return "not mergeable"
}

// mergeMethod returns the configured PR merge method, defaulting to
// "squash" (how these repos merge).
func mergeMethod() string {
	if m := viper.GetString("code_host.merge_method"); m != "" {
		return m
	}
	return "squash"
}

// offerPRMerges is the pre-release merge pre-check (structurally like the
// push offer): before the release plan is built, find each repo's
// unmerged-but-mergeable workspace PR and, interactively, offer to merge
// them. Merging here — rather than as a plan Action — is required
// because the release gate and the tag T are computed from merge state,
// which must be settled before Assess runs. Merged PRs then flow into
// selectReleaseTargets as releasable; anything left unmerged is blocked
// by the gate. Never auto-merges: non-interactive runs skip entirely.
func offerPRMerges(ctx context.Context, host code.Host, targets []releaseTarget) error {
	if host == nil || !isInteractive() {
		return nil
	}

	type candidate struct {
		idx    int
		number int
		name   string
	}
	var candidates []candidate
	if _, err := ui.RunCardSteps([]ui.CardStep{{
		Card: ui.NewCard(ui.CardRunning, "releases").Raw("Checking pull requests..."),
		Run: func() error {
			for i := range targets {
				rt := &targets[i]
				if rt.tagErr != nil {
					continue
				}
				pr, perr := host.GetPRForBranch(ctx, rt.owner, rt.repoName, rt.branch)
				if perr != nil {
					continue // best-effort — the gate handles unknowns
				}
				if pr.Number > 0 && pr.State != "merged" && canMergePR(pr.MergeableState) {
					candidates = append(candidates, candidate{i, pr.Number, rt.repo.Name})
				}
			}
			return nil
		},
	}}, nil); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	noun := "pull request"
	if len(candidates) > 1 {
		noun = "pull requests"
	}
	confirmed, err := NewDialog("Merge pull requests?").
		Description(fmt.Sprintf("%d approved %s ready to merge. Merge before releasing?", len(candidates), noun)).
		Affirmative("Merge").
		Negative("Skip").
		Default(true).
		Show()
	if err != nil {
		return err
	}
	if !confirmed {
		return nil // declined → those repos stay unmerged → blocked by the gate
	}

	// A merge failure is soft: surfaced on its row, and the repo simply
	// stays unmerged and gets blocked below — never aborts the others.
	method := mergeMethod()
	ui.RunGroup("merging", func(grp ui.Reporter) {
		for _, c := range candidates {
			name := ui.PreserveCase(c.name)
			merr := grp.Spinner(name, func() error {
				_, e := host.MergePR(ctx, targets[c.idx].owner, targets[c.idx].repoName, c.number, method)
				return e
			})
			if merr != nil {
				grp.FailValue(name, merr.Error())
				continue
			}
			grp.CompleteValue(name, fmt.Sprintf("#%d merged", c.number))
		}
	})
	return nil
}

// versionEligible reports whether rt would be a release candidate on
// version/sweep-up grounds alone — before the release gate is applied:
// its lookup succeeded, the derived next version differs from the latest
// tag, and no existing release already contains its work. Used to scope
// gate-reason rendering to repos that would otherwise release (so an
// already-current or already-shipped repo shows its tag, not a gate
// note).
func (rt *releaseTarget) versionEligible() bool {
	if rt.containingRelease != nil {
		// Another release already contains our work — don't cut a
		// duplicate. The notify path handles announcing it if Slack
		// doesn't already know.
		return false
	}
	return rt.tagErr == nil && rt.nextVersion != "" && rt.nextVersion != rt.currentTag
}

// eligible reports whether rt is a candidate for a new release: it
// clears the version/sweep-up pre-filters AND the release gate confirms
// the workspace's work is on the default branch. Repos already at the
// target version, with errors, already released elsewhere, or whose work
// isn't merged are not offered in the selection form.
func (rt *releaseTarget) eligible() bool {
	return rt.versionEligible() && rt.gate == gateAllow
}

// preselect reports whether an eligible repo should be pre-checked in
// the selection form (and included by default when no form shows). Every
// eligible repo pre-checks — the gate already removed the not-on-default
// repos, so there's no remaining opt-in edge case.
func (rt *releaseTarget) preselect() bool { return rt.eligible() }

// producesResult reports whether this repo lands a row in
// releaseResults — i.e. something the notification would announce: a
// release we'll create (include), a sweep-up we'll point at
// (containingRelease), or an already-at-current-version listing.
// Mirrors the branches of the release action's Assess/Apply that append
// to releaseResults, computed from target state so the notify action
// can predict an empty announcement before any Apply runs. Blocked,
// skipped, deselected, and errored repos produce nothing.
func (rt *releaseTarget) producesResult() bool {
	if rt.tagErr != nil {
		return false
	}
	if rt.include || rt.containingRelease != nil {
		return true
	}
	return rt.currentTag != "" && rt.nextVersion == rt.currentTag
}

// infoRowNote returns the reason an inert (non-selectable) row carries
// in the form and result card: the containing-release tag for sweep-up
// repos, else the release gate's reason for blocked/skipped repos.
func (rt *releaseTarget) infoRowNote() string {
	if rt.containingRelease != nil {
		note := "in " + rt.containingRelease.Tag
		if rt.containingRelease.AuthorLogin != "" {
			note += " (by @" + rt.containingRelease.AuthorLogin + ")"
		}
		return note
	}
	return rt.gateReason
}

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

			detail := emitLifecyclePreamble(ctx, issue)

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

			// Pre-check: offer to merge unmerged-but-mergeable workspace
			// PRs before the release plan is built, so the gate + tag T
			// see settled merge state. Mirrors the push offer's position.
			if err := offerPRMerges(ctx, host, targets); err != nil {
				return err
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

				// isExisting is true when this result represents a
				// release another user cut whose tag already contains
				// our work (sweep-up case). Notify Apply consults
				// HasAnnouncement before re-posting and skips when an
				// announcement is already in the channel.
				isExisting bool

				// extraPRs carries the "this release also includes
				// work by these contributors" list, surfaced in both
				// the result card and the notification.
				extraPRs []code.PullRequest
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
								// Containing-release: another user already
								// cut a release whose tag contains our work
								// (sweep-up). Capture it as a result so the
								// notify path can check Slack and announce
								// if missing. Render as "in v1.2.4" (the
								// existing tag).
								if rt.containingRelease != nil {
									releaseResults = append(releaseResults, releaseResult{
										repo:       rt.repo.Name,
										services:   rt.services,
										subjects:   rt.subjects,
										release:    *rt.containingRelease,
										version:    rt.containingRelease.Tag,
										isExisting: true,
										extraPRs:   rt.extraPRs,
									})
									detail := "in " + rt.containingRelease.Tag
									if rt.containingRelease.AuthorLogin != "" {
										detail += " (by @" + rt.containingRelease.AuthorLogin + ")"
									}
									return ActionCompleted, detail, nil
								}
								// Blocked or skipped by the release gate — the
								// work isn't on the default branch, or there's
								// nothing of ours to release. Not announced.
								// Plan-detail grammar: persisting tag first
								// (when known), the gate reason parenthesized
								// — `= v1.2.3 (PR #42 open — not merged)`.
								// Must precede the already-current branch
								// below, which would otherwise mislabel a
								// version-eligible-but-blocked repo.
								if rt.versionEligible() && rt.gate != gateAllow {
									detail := "(" + rt.gateReason + ")"
									if rt.currentTag != "" {
										detail = rt.currentTag + " " + detail
									}
									return ActionCompleted, detail, nil
								}
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
								// Deselected-eligible: persisting tag first,
								// why parenthesized (plan-detail grammar).
								if rt.currentTag != "" {
									return ActionCompleted, rt.currentTag + " (not selected)", nil
								}
								return ActionCompleted, "(not selected)", nil
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
							// Tag default-branch HEAD: the release must contain
							// the workspace's work, and the gate already verified
							// it's on the default branch. Tagging HEAD (rather
							// than a specific commit) also gives
							// generate_release_notes full ancestry, so the
							// changelog is never empty. Falls back to the branch
							// only if the default couldn't be resolved — the gate
							// then defaulted to permissive-allow.
							target := rt.branch
							if rt.defaultBranch != "" {
								target = rt.defaultBranch
							}
							// Pin the changelog baseline to the tag we
							// resolved as "latest" — GitHub's own pick uses
							// /releases/latest, which can miss intermediate
							// tags that weren't marked latest and produce
							// nonsense ranges like v4.19.130...v4.19.142 with
							// an empty body.
							rel, err := host.CreateRelease(ctx, code.CreateReleaseRequest{
								Owner:         rt.owner,
								Repository:    rt.repoName,
								Tag:           rt.nextVersion,
								Target:        target,
								Name:          rt.nextVersion,
								GenerateNotes: true,
								PreviousTag:   rt.currentTag,
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
								extraPRs: rt.extraPRs,
							})
							return nil
						},
					})
				}
			}

			// Status + notify gates share these rollups. Status advances
			// to ready_for_release UNLESS nothing releases AND something is
			// blocked (an unmerged PR that didn't merge) — mirrors the
			// release command's gate, so an unmerged PR no longer marks
			// the issue ready while shipping nothing. All-already-current
			// still advances. Notify no-ops when nothing produces a result.
			anyReleaseOutcome, anyBlocked := false, false
			for i := range targets {
				rt := &targets[i]
				if rt.producesResult() {
					anyReleaseOutcome = true
				}
				if rt.versionEligible() && rt.gate == gateBlock {
					anyBlocked = true
				}
			}

			tracker, _ := newIssueTracker()
			if !(anyBlocked && !anyReleaseOutcome) {
				if sa, ok := statusAction(tracker, issue, detail.Status, "ready_for_release"); ok {
					actions = append(actions, sa)
				}
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
						if !anyReleaseOutcome {
							return ActionCompleted, "nothing to announce", nil
						}
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
						items := make([]notify.Item, 0, len(releaseResults))
						for _, r := range releaseResults {
							// Containing-release rows (sweep-up): another
							// user cut the release. Skip the item when
							// Slack already has an announcement for that
							// URL — avoids duplicate posts when multiple
							// users run prerelease for overlapping work.
							// Soft-fail on HasAnnouncement errors: post
							// conservatively so we never silently miss
							// an announcement on a transient lookup hiccup.
							if r.isExisting {
								if r.release.URL == "" {
									// Assumed-contained: sweep-up couldn't
									// confirm the release object (non-404
									// lookup error), so we skipped cutting a
									// release but have no URL to announce.
									// Nothing to post for this repo.
									continue
								}
								// excludeIssueKey lets re-runs in this
								// workspace fall through to Notify (which
								// upserts our own prior message); only a
								// different user's announcement skips.
								found, _ := releaseNotifier.HasAnnouncement(ctx, releaseChannel, r.release.URL, issue)
								if found {
									continue
								}
							}
							// formatSubjects collapses to repo name when the
							// user kept (or seeded) all services, so the team's
							// "we shipped everything in this repo" announcement
							// renders as `` `host-ui`: <url> `` rather than a
							// long comma list. The "`, `" separator lives
							// between adjacent service names inside the
							// template's outer backticks, producing
							//     going out `svc-a`, `svc-b`: <url>
							// for the partial-selection case.
							items = append(items, notify.Item{
								Label:  formatSubjects(r.repo, r.services, r.subjects, "`, `"),
								URL:    r.release.URL,
								Detail: r.version,
								Body:   r.release.Body, // host-generated release notes
							})
						}
						if len(items) == 0 {
							// Everything was already announced — nothing to
							// post. Returning nil here lets the action card
							// finalize as Completed without sending.
							return nil
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

				// Multi-user awareness: detect whether someone else
				// already cut a release whose range includes our HEAD,
				// and enumerate any "extra" PRs (work by other
				// contributors) that will get swept into a release we
				// create. See ROADMAP / plan for the framing.
				resolveMultiUserContext(ctx, g, host, rt)
				// Release gate: is the workspace's work on the default
				// branch? Runs after the above so it can read the PR
				// state and default branch it resolved.
				resolveReleaseGate(ctx, g, rt)
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
		serviceIdx int  // -1 = fallback "no services configured" row; -2 = containing-release info row
		preselect  bool
	}
	const infoOnlyServiceIdx = -2
	var rows []optRow
	for i := range targets {
		rt := &targets[i]
		// Containing-release repos surface as info-only rows in the
		// form: visible (so the user sees we considered them) with
		// the containing tag appended, but inert — any toggle is
		// ignored at parse time below. Keeps the picker self-contained
		// rather than pushing the user to look at two places for the
		// release plan.
		if rt.containingRelease != nil {
			rows = append(rows, optRow{repoIdx: i, serviceIdx: infoOnlyServiceIdx, preselect: false})
			continue
		}
		// Gate-blocked/skipped repos surface the same way — an inert
		// info row with the reason — so the user sees we considered the
		// repo and why it's out, without it being selectable.
		if rt.versionEligible() && rt.gate != gateAllow {
			rows = append(rows, optRow{repoIdx: i, serviceIdx: infoOnlyServiceIdx, preselect: false})
			continue
		}
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
		switch {
		case row.serviceIdx == infoOnlyServiceIdx:
			// Inert info row (sweep-up or gate-blocked/skipped): append
			// the reason so the user sees why the repo is out. The pick
			// is parsed back to a no-op below.
			label += " · " + rt.infoRowNote()
		case row.serviceIdx >= 0:
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
		// Info-only rows (containing-release repos) are shown in the
		// form for visibility but can't be selected for release —
		// silently no-op any toggle the user makes on them.
		if si == infoOnlyServiceIdx || rt.containingRelease != nil {
			continue
		}
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
		case rt.containingRelease != nil:
			// Sweep-up: another release already contains our work.
			// Render as a skipped row with "in <tag>" + author so the
			// user sees who shipped it.
			nSkip++
			rows = append(rows, row{name, glyphOff, off(name, rt.infoRowNote())})
		case rt.versionEligible() && rt.gate != gateAllow:
			// Blocked or skipped by the release gate (work not on the
			// default branch, or nothing of ours to release). Skipped
			// row carrying the gate reason.
			nSkip++
			rows = append(rows, row{name, glyphOff, off(name, rt.gateReason)})
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
	// Build a sorted lookup so extras-continuation rows land
	// immediately under each repo's primary row, preserving the
	// alphabetical sort the rows already follow.
	extrasByRepo := make(map[string]string, len(targets))
	for i := range targets {
		rt := &targets[i]
		if line := formatExtrasNote(rt.extraPRs); line != "" {
			extrasByRepo[rt.repo.Name] = line
		}
	}
	extrasGlyph := muted.Render("+")
	for _, r := range rows {
		card.Item(r.glyph, r.content)
		if extras, ok := extrasByRepo[r.repo]; ok {
			card.Item(extrasGlyph, muted.Render(extras))
		}
	}
	return card
}

// formatExtrasNote composes the muted continuation row shown under a
// repo when other PRs are bundled into the release range. Empty
// result → no continuation row. Truncates to keep the line readable.
func formatExtrasNote(prs []code.PullRequest) string {
	if len(prs) == 0 {
		return ""
	}
	const limit = 3
	parts := make([]string, 0, limit)
	for i, pr := range prs {
		if i >= limit {
			break
		}
		entry := fmt.Sprintf("#%d", pr.Number)
		if pr.AuthorLogin != "" {
			entry += " @" + pr.AuthorLogin
		}
		parts = append(parts, entry)
	}
	out := "also " + strings.Join(parts, ", ")
	if len(prs) > limit {
		out += fmt.Sprintf(", and %d more", len(prs)-limit)
	}
	return out
}

// releaseTagPattern matches release-shaped tags so resolveMultiUserContext
// can ignore non-release tags (feature flags, infra refs, etc.) when
// walking TagsContaining results. Capture groups expose the
// major/minor/patch components for compareSemverTag's ordering.
var releaseTagPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// compareSemverTag orders two release-shaped tags by their
// (major, minor, patch) triple. Returns -1 if a < b, 0 if equal,
// 1 if a > b. Non-matches sort to the end (treated as -1, -1, -1).
// Used to find the lowest-semver containing release (the one that
// first shipped a given commit) when multiple tags match.
func compareSemverTag(a, b string) int {
	parse := func(s string) (int, int, int, bool) {
		m := releaseTagPattern.FindStringSubmatch(s)
		if m == nil {
			return 0, 0, 0, false
		}
		major, _ := strconv.Atoi(m[1])
		minor, _ := strconv.Atoi(m[2])
		patch, _ := strconv.Atoi(m[3])
		return major, minor, patch, true
	}
	aM, am, ap, aok := parse(a)
	bM, bm, bp, bok := parse(b)
	if !aok && !bok {
		return strings.Compare(a, b)
	}
	if !aok {
		return 1
	}
	if !bok {
		return -1
	}
	if aM != bM {
		if aM < bM {
			return -1
		}
		return 1
	}
	if am != bm {
		if am < bm {
			return -1
		}
		return 1
	}
	if ap != bp {
		if ap < bp {
			return -1
		}
		return 1
	}
	return 0
}

// resolveMultiUserContext checks for sweep-up / containing-release
// scenarios and enumerates "extra" PRs in the release range that
// aren't the workspace's own work. Populates rt.containingRelease,
// rt.extraPRs, and rt.workspacePRNumber as a side effect. Failures
// (network errors, etc.) are swallowed — multi-user awareness is a
// nice-to-have, not load-bearing for the release itself.
func resolveMultiUserContext(ctx context.Context, g vcs.VCS, host code.Host, rt *releaseTarget) {
	// Look up the workspace's own PR for this repo. We need:
	//   - its Number, to exclude from extras
	//   - its MergeCommitSHA, to drive containing-release detection
	//     when the PR was squash-merged (the merge commit is on
	//     main; the local branch's commits aren't — `git tag
	//     --contains <localHEAD>` returns nothing useful, but
	//     `--contains <mergeCommitSHA>` finds tags that include the
	//     merged work).
	var workspacePR code.PullRequest
	if pr, err := host.GetPRForBranch(ctx, rt.owner, rt.repoName, rt.branch); err == nil {
		workspacePR = pr
		rt.workspacePRNumber = pr.Number
		rt.workspacePRState = pr.State
		rt.workspacePRMergeableState = pr.MergeableState
		rt.workspacePRReview = pr.Review
	}

	// Default branch — the release tag target (CreateRelease), the base
	// for the gate's on-default ancestry check, and the extras range
	// endpoint. Best-effort: an empty value makes the gate permissive
	// and the extras range fall back to the branch.
	if def, err := g.GetDefaultBranch(ctx, rt.repo.Path); err == nil {
		rt.defaultBranch = def
	}

	// Refresh tags so containing-release detection sees anything
	// pushed by other users in the last few minutes. Best-effort —
	// a fetch failure just means we work with whatever's local.
	_ = g.FetchTags(ctx, rt.repo.Path, "origin")

	// Choose the SHA to ask "what release tag contains this?". Order
	// of preference:
	//   1. The workspace PR's MergeCommitSHA when the PR is merged.
	//      Required for squash- and rebase-merge cases where the
	//      committed-on-main SHA differs from any commit on the local
	//      branch — without this we'd miss the sweep-up.
	//   2. Local HEAD, for unmerged branches (preview / staging
	//      scenarios where the workspace is mid-flight).
	probeSHA := ""
	if workspacePR.State == "merged" && workspacePR.MergeCommitSHA != "" {
		probeSHA = workspacePR.MergeCommitSHA
	} else if headSHA, err := g.HeadSHA(ctx, rt.repo.Path); err == nil {
		probeSHA = headSHA
	}

	if probeSHA != "" {
		tags, err := g.TagsContaining(ctx, rt.repo.Path, probeSHA)
		if err == nil {
			// A repo can have semver tags WITHOUT a corresponding
			// GitHub release record (e.g. a tag pushed without a
			// release, or the release was deleted). Walk every
			// release-shaped containing tag and pick the first one
			// that resolves to an actual published release —
			// otherwise a tag-without-release shadows a release
			// that ALSO contains the work and we'd miss the
			// sweep-up. We want the lowest-semver containing
			// release (the one that first shipped this work), so
			// sort ascending before walking.
			//
			// Note: we do NOT filter out rt.currentTag here.
			// currentTag is "the current latest release on the host"
			// (from GetLatestTag) — which IS the sweep-up tag in
			// the common case where the merge has already been
			// released. Filtering it out would skip the correct
			// answer and pick a stale tag-without-release instead.
			candidates := releaseTagsInSemverOrder(tags)
			unconfirmedTag := ""
			for _, t := range candidates {
				rel, err := host.GetReleaseByTag(ctx, rt.owner, rt.repoName, t)
				if err == nil {
					rt.containingRelease = &rel
					break
				}
				if !errors.Is(err, code.ErrNotFound) {
					// Network/auth error — we can't confirm this tag is a
					// published release, but it IS a release-shaped tag
					// that contains our merged commit. Remember it and
					// stop rather than walking into more failing calls.
					unconfirmedTag = t
					break
				}
				// 404 on this tag — a tag without a release; keep looking.
			}
			// Conservative fallback: our merged commit sits in a
			// release-shaped tag, but a non-404 error stopped us
			// confirming the release object. Assume it's already shipped
			// and skip cutting a redundant/near-empty release rather than
			// proceeding as if nothing contained it. The synthetic
			// release carries only the tag (no URL) — enough to render
			// "in <tag>"; the notify path skips URL-less results so we
			// never announce a release we couldn't fetch.
			if rt.containingRelease == nil && unconfirmedTag != "" {
				rt.containingRelease = &code.Release{Tag: unconfirmedTag}
			}
		}
	}

	// Enumerate extras: PRs merged into the default branch in the
	// release range that aren't the workspace's own PR. Skip when no
	// currentTag exists (first release — there's nothing to compare
	// against; the whole history would show as "extras", which isn't
	// useful framing).
	if rt.currentTag == "" {
		return
	}
	var exclude []int
	if rt.workspacePRNumber > 0 {
		exclude = []int{rt.workspacePRNumber}
	}
	// The extras range ends where the tag lands — default-branch HEAD —
	// so the "also ships PRs by @others" list matches exactly what this
	// release covers. Falls back to the branch when the default isn't
	// known; the containing tag stands in for the sweep-up case.
	headRef := rt.branch
	if rt.defaultBranch != "" {
		headRef = rt.defaultBranch
	}
	if rt.containingRelease != nil {
		headRef = rt.containingRelease.Tag
	}
	if prs, err := host.PRsInRange(ctx, rt.owner, rt.repoName, rt.currentTag, headRef, exclude); err == nil {
		rt.extraPRs = prs
	}
}

// resolveReleaseGate sets rt.gate/gateReason — the decision on whether
// the workspace's work is on the default branch and may be released.
// Runs after resolveMultiUserContext (which fills the PR fields and
// defaultBranch). Best-effort and permissive: when the PR state or the
// ancestry check can't be determined, it defaults to allow rather than
// blocking a release on a transient failure.
func resolveReleaseGate(ctx context.Context, g vcs.VCS, rt *releaseTarget) {
	if !rt.versionEligible() {
		return // nothing to gate — version/sweep-up already excludes it
	}
	if rt.workspacePRNumber > 0 {
		// A PR exists: its merged-ness is the definitive signal (and
		// covers the squash case, which always has a PR).
		gate, reason := classifyReleaseGate(rt.workspacePRNumber, rt.workspacePRState, false)
		// Enrich the unmerged-PR block with *why* it can't merge (the
		// reason the merge pre-check couldn't offer/complete it), instead
		// of the generic "not merged".
		if gate == gateBlock {
			reason = fmt.Sprintf("PR #%d · %s", rt.workspacePRNumber,
				prMergeBlockReason(rt.workspacePRMergeableState, rt.workspacePRReview))
		}
		// Empty-release guard: an allowed (merged) repo whose default
		// branch is already fully contained in the latest tag has nothing
		// new to ship — the work was released already (e.g. by someone
		// else) and sweep-up couldn't confirm it (empty MergeCommitSHA,
		// or a non-404 lookup blip). Skip rather than cut an empty tag.
		if gate == gateAllow && rt.currentTag != "" && rt.defaultBranch != "" {
			_ = g.Fetch(ctx, rt.repo.Path, "origin", rt.defaultBranch)
			if shipped, err := g.IsMergedInto(ctx, rt.repo.Path, "origin/"+rt.defaultBranch, rt.currentTag); err == nil {
				gate, reason = applyEmptyReleaseGuard(gate, reason, rt.currentTag, shipped)
			}
		}
		rt.gate, rt.gateReason = gate, reason
		return
	}
	// No PR — split "nothing beyond default" (skip) from "unmerged
	// commits, no PR" (block) via an on-default-branch ancestry check.
	if rt.defaultBranch == "" {
		rt.gate = gateAllow // can't determine default → don't block
		return
	}
	_ = g.Fetch(ctx, rt.repo.Path, "origin", rt.defaultBranch)
	onDefault, err := g.IsMergedInto(ctx, rt.repo.Path, rt.branch, "origin/"+rt.defaultBranch)
	if err != nil {
		rt.gate = gateAllow // can't verify ancestry → don't block
		return
	}
	rt.gate, rt.gateReason = classifyReleaseGate(0, "", onDefault)
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
