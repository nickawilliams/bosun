package cli

import (
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
)

// statusKVWidth is the column width used to pad row labels in repo
// card body rows so the dot separator aligns. Sized for the widest
// label currently in use: "Preview" (7 chars). Others — "Status",
// "Branch", "Checks", "Repos", "PR" — pad to the same width.
const statusKVWidth = 7

// statusRowKV composes a body row's content as
// "<padded muted key> · <value>", mirroring Card.KV's default
// styling so plan-style rows align on the dot separator. Labels
// wider than statusKVWidth render without padding (so the dot
// shifts right) rather than panicking on negative repeat count.
func statusRowKV(key, value string) string {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	pad := statusKVWidth - lipgloss.Width(key)
	if pad < 0 {
		pad = 0
	}
	padded := key + strings.Repeat(" ", pad)
	return muted.Render(padded) + " " + muted.Render(ui.Palette.Dot) + " " + value
}

// statusStyledGlyph applies a foreground color to a glyph token.
// Helpers return (glyph, color); this wrapper renders them into a
// single styled string for Card.Item.
func statusStyledGlyph(glyph string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(glyph)
}

// statusCardStateColor returns the palette role color associated
// with a resolved aggregate card state — the same hue used by the
// gutter glyph — so a card's title can be tinted to match.
//
// State-context call site — see state_grammar.go. CardSuccess maps
// to RoleDone (purple) because in the status context, CardSuccess
// represents a terminal-positive resolution (e.g., merged-PR rollup),
// not just "an action just succeeded".
func statusCardStateColor(state ui.CardState) color.Color {
	switch state {
	case ui.CardSuccess:
		return ui.Palette.RoleDone
	case ui.CardReady:
		return ui.Palette.RoleOpen
	case ui.CardSkipped:
		return ui.Palette.RoleAttention
	case ui.CardWaiting:
		return ui.Palette.RoleInFlight
	case ui.CardFailed:
		return ui.Palette.RoleClosed
	}
	return ui.Palette.Primary
}

// repoCardGlyphVisual returns the (CardState, color) pair used to
// render a repo card's gutter glyph + title color under the
// state-context grammar (state_grammar.go). Most states render as ●
// colored to the appropriate role; the two terminal PR outcomes use
// their event-shaped glyphs (✓ purple for merged, ✗ red for closed).
//
// Kept distinct from resolveRepoCardState below, which retains its
// 5-bucket CardState distinctions for the workspace and project
// tally code (CardSuccess / CardReady / CardSkipped / CardWaiting /
// CardFailed map to done / ready / blocked / pending / broken in
// the summary). Two functions, two jobs: one for rendering the row,
// one for categorizing it.
func repoCardGlyphVisual(branchState string, pr code.PullRequest, checks code.CheckRollup) (ui.CardState, color.Color) {
	switch pr.State {
	case "merged":
		return ui.CardSuccess, ui.Palette.RoleDone
	case "closed":
		return ui.CardFailed, ui.Palette.RoleClosed
	case "draft":
		return ui.CardReady, ui.Palette.RoleNeutral
	}
	if strings.HasPrefix(branchState, "diverged") || strings.HasPrefix(branchState, "behind") {
		return ui.CardReady, ui.Palette.RoleAttention
	}
	if pr.State == "open" {
		switch pr.MergeableState {
		case "clean", "has_hooks":
			return ui.CardReady, ui.Palette.RoleOpen
		case "blocked":
			if prWaitingOnOthers(pr, checks) {
				return ui.CardReady, ui.Palette.RoleInFlight
			}
			return ui.CardReady, ui.Palette.RoleAttention
		case "dirty", "behind", "unstable":
			return ui.CardReady, ui.Palette.RoleAttention
		}
		return ui.CardReady, ui.Palette.RoleInFlight
	}
	return ui.CardReady, ui.Palette.RoleInFlight
}

// prWaitingOnOthers reports whether an open PR's "blocked"
// mergeable_state is a benign wait rather than a user-actionable
// block: required checks still running, or a requested review not yet
// submitted (with no checks failing). Mirrors the two in-flight
// branches of statusPRDominant's blocked case so the repo card's
// glyph/tally and the PR row's label always agree on which side of
// the pending/blocked line a PR falls.
func prWaitingOnOthers(pr code.PullRequest, checks code.CheckRollup) bool {
	return checks.State == "running" ||
		(pr.Review == "awaiting" && checks.State != "failing")
}

// resolveRepoCardState maps a repo's branch + PR state onto the
// 5-state aggregate vocabulary used by the workspace tally code.
// Mirrors the scratchpad's resolveWstatRepoCardState. Precedence:
// terminal PR states (merged → done, closed → broken) win; then
// branch problems (diverged / behind) → blocked; then PR
// mergeability (clean → ready, dirty/behind/unstable → blocked,
// blocked → pending when the PR is just waiting on others — see
// prWaitingOnOthers — otherwise blocked); else pending. Used for
// counting only — glyph rendering for repo cards goes through
// repoCardGlyphVisual above.
func resolveRepoCardState(branchState string, pr code.PullRequest, checks code.CheckRollup) ui.CardState {
	switch pr.State {
	case "merged":
		return ui.CardSuccess
	case "closed":
		return ui.CardFailed
	case "draft":
		return ui.CardWaiting
	}
	if strings.HasPrefix(branchState, "diverged") || strings.HasPrefix(branchState, "behind") {
		return ui.CardSkipped
	}
	if pr.State == "open" {
		switch pr.MergeableState {
		case "clean":
			return ui.CardReady
		case "blocked":
			if prWaitingOnOthers(pr, checks) {
				return ui.CardWaiting
			}
			return ui.CardSkipped
		case "dirty", "behind", "unstable":
			return ui.CardSkipped
		}
		return ui.CardWaiting
	}
	return ui.CardWaiting
}

// branchStateString maps a vcs.BranchSync into the spec's branch
// state vocabulary ("in sync" | "ahead N" | "behind N" |
// "diverged X/Y" | "unpushed N").
func branchStateString(sync vcs.BranchSync) string {
	if !sync.HasRemote {
		return fmt.Sprintf("unpushed %d", sync.Ahead)
	}
	switch {
	case sync.Ahead == 0 && sync.Behind == 0:
		return "in sync"
	case sync.Ahead > 0 && sync.Behind > 0:
		return fmt.Sprintf("diverged %d/%d", sync.Ahead, sync.Behind)
	case sync.Ahead > 0:
		return fmt.Sprintf("ahead %d", sync.Ahead)
	default:
		return fmt.Sprintf("behind %d", sync.Behind)
	}
}

// statusBranchGlyph returns a 3-cell glyph token for the branch row:
// 1 char glyph + 2 cells for an optional commit count (single digit
// + space, "9+" for double-digit, or two spaces when no count
// applies). Keeps repo cards' glyph columns aligned across the run.
//
// State-context call site — see state_grammar.go. The directional
// glyphs (↕ ↑ ↓ +) are domain-specific state markers that encode
// info ● can't carry (direction + commit count); the grammar allows
// these specialized state glyphs. Only "in sync" uses the default
// ● state glyph since there's no count to carry there.
func statusBranchGlyph(sync vcs.BranchSync) (string, color.Color) {
	if !sync.HasRemote {
		// + means "additions" — N local commits ahead of base, no
		// remote yet. Pairs cleanly with digits and reads directly
		// as "N to push."
		return "+" + countToken(sync.Ahead), ui.Palette.RoleNeutral
	}
	switch {
	case sync.Ahead == 0 && sync.Behind == 0:
		return ui.Palette.Active + "  ", ui.Palette.RoleOpen
	case sync.Ahead > 0 && sync.Behind > 0:
		// Sum of ahead + behind — magnitude of divergence, not its
		// split. The glyph says "two-way", the number says "this
		// much to reconcile."
		return "↕" + countToken(sync.Ahead+sync.Behind), ui.Palette.RoleClosed
	case sync.Ahead > 0:
		return "↑" + countToken(sync.Ahead), ui.Palette.RoleInFlight
	default:
		return "↓" + countToken(sync.Behind), ui.Palette.RoleAttention
	}
}

// countToken renders an integer in 2 cells: "N " for 1–9, "9+" for
// any value ≥ 10, "  " for 0 / unknown. The trailing pad keeps the
// glyph token at a fixed width.
func countToken(n int) string {
	switch {
	case n <= 0:
		return "  "
	case n > 9:
		return "9+"
	default:
		return fmt.Sprintf("%d ", n)
	}
}

// statusBranchRow returns the (glyph, value) pair for a repo card's
// branch row. Glyph color carries the sync state; value is the
// branch name (linked via OSC 8 to its host URL when known) with
// a muted `*` suffix when the working tree is dirty.
func statusBranchRow(branch, branchURL string, sync vcs.BranchSync, dirty bool) (string, string) {
	glyph, c := statusBranchGlyph(sync)
	g := statusStyledGlyph(glyph, c)
	v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(branch)
	if branchURL != "" {
		v = osc8Link(branchURL, v)
	}
	if dirty {
		v += lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("*")
	}
	return g, v
}

// statusPRRow returns the (glyph, value) pair for a repo card's PR
// row. Value is the PR number (linked) followed by the unified
// 6-state display label in parens: merged / approved / open /
// changes_requested / draft / closed (folding review state into
// "open" subdivisions).
func statusPRRow(pr code.PullRequest, checks code.CheckRollup) (string, string) {
	if pr.Number == 0 {
		// No PR exists — muted "(none)".
		glyph := statusStyledGlyph(statusPRGlyph("", "", "", code.CheckRollup{}))
		return glyph, lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("(none)")
	}

	label, col, glyphChar := statusPRDominant(pr.State, pr.MergeableState, pr.Review, checks)
	glyph := statusStyledGlyph(glyphChar+"  ", col)

	number := fmt.Sprintf("#%d", pr.Number)
	v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(number)
	if pr.URL != "" {
		v = osc8Link(pr.URL, v)
	}
	if label != "" {
		styled := lipgloss.NewStyle().Foreground(col).Render("(" + label + ")")
		v += " " + styled
	}
	return glyph, v
}

// statusPRDominant folds state, mergeable_state, review state, and
// the check rollup into a single dominant condition: a parenthetical
// label, a palette color, and the bare glyph character. The same
// condition drives all three, so the glyph color and parenthetical
// color always agree — "approved" no longer reads green next to a
// warning glyph because the row is behind base, has changes
// requested, or is awaiting required checks.
//
// The check rollup disambiguates the "blocked" mergeable_state: when
// at least one required check is still running (no failures yet), the
// PR is awaiting CI rather than truly blocked, so the row renders as
// in-flight instead of attention.
//
// Per the state-context grammar (state_grammar.go), only the two
// terminal PR outcomes keep their event-shaped glyphs:
//
//	merged → ✓ + RoleDone (purple)  — terminal positive resolution
//	closed → ✗ + RoleClosed (red)   — terminal negative resolution
//
// Every other case is a current state, so the row uses ● and the
// color carries the role (RoleOpen / RoleAttention / RoleInFlight /
// RoleNeutral). Glyph and color always agree by construction —
// removing the "approved is green but glyph is yellow" class of
// inconsistency we hit before.
//
// Precedence for open PRs (high → low):
//
//  1. review = changes_requested  → "changes requested" / attention
//  2. mergeable = blocked + checks running
//     → "required checks pending" / in-flight
//  3. mergeable = blocked + review = awaiting + checks not failing
//     → "awaiting review" / in-flight
//  4. mergeable in {dirty, behind, unstable, blocked}
//     → mapped label / attention
//  5. mergeable in {clean, has_hooks} + review = approved
//     → "approved" / open
//  6. mergeable in {clean, has_hooks} + review = awaiting
//     → "awaiting review" / in-flight
//  7. mergeable = unknown → "unknown" / neutral
//  8. else → "open" / in-flight
func statusPRDominant(state, mergeableState, reviewState string, checks code.CheckRollup) (label string, col color.Color, glyph string) {
	switch state {
	case "merged":
		return "merged", ui.Palette.RoleDone, ui.Palette.Check
	case "draft":
		return "draft", ui.Palette.RoleNeutral, ui.Palette.Active
	case "closed":
		return "closed", ui.Palette.RoleClosed, ui.Palette.Cross
	case "open":
		if reviewState == "changes_requested" {
			return "changes requested", ui.Palette.RoleAttention, ui.Palette.Active
		}
		switch mergeableState {
		case "dirty":
			return "conflicts", ui.Palette.RoleAttention, ui.Palette.Active
		case "behind":
			return "behind base", ui.Palette.RoleAttention, ui.Palette.Active
		case "unstable":
			return "checks failing", ui.Palette.RoleAttention, ui.Palette.Active
		case "blocked":
			// "blocked" most often means "required check failing or
			// missing", but it also covers "required check still
			// running" — same opaque enum either way. If the check
			// rollup says we're mid-run with nothing failing, surface
			// that as an in-flight signal instead of attention.
			if checks.State == "running" {
				return "required checks pending", ui.Palette.RoleInFlight, ui.Palette.Active
			}
			// The other benign "blocked" cause: a requested review not
			// yet submitted. The author has done their part — the wait
			// is on the reviewer, who isn't necessarily the person
			// running status — so it reads as in-flight, not attention.
			// Only a *requested* review softens the state ("" means no
			// reviewer was asked, and asking is on the user), and
			// failing checks still dominate.
			if reviewState == "awaiting" && checks.State != "failing" {
				return "awaiting review", ui.Palette.RoleInFlight, ui.Palette.Active
			}
			return "blocked", ui.Palette.RoleAttention, ui.Palette.Active
		case "unknown":
			return "unknown", ui.Palette.RoleNeutral, ui.Palette.Active
		case "clean", "has_hooks":
			if reviewState == "approved" {
				return "approved", ui.Palette.RoleOpen, ui.Palette.Active
			}
			// Mergeable without a required review, but one was
			// requested — say where the PR actually sits instead of
			// the generic "open". Same in-flight role either way.
			if reviewState == "awaiting" {
				return "awaiting review", ui.Palette.RoleInFlight, ui.Palette.Active
			}
			return "open", ui.Palette.RoleInFlight, ui.Palette.Active
		}
		return "open", ui.Palette.RoleInFlight, ui.Palette.Active
	}
	// No PR — caller short-circuits before reaching here; provide a
	// safe in-flight glyph for the empty-state path.
	return "", ui.Palette.RoleInFlight, ui.Palette.Active
}

// statusPRGlyph returns the 3-cell glyph token for the PR row.
// Thin wrapper over statusPRDominant so the existing call sites
// (and the glyph unit tests) keep their shape.
func statusPRGlyph(state, mergeableState, reviewState string, checks code.CheckRollup) (string, color.Color) {
	_, col, glyph := statusPRDominant(state, mergeableState, reviewState, checks)
	return glyph + "  ", col
}

// statusChecksRow returns the (glyph, value) pair for a repo card's
// Checks row. Value is the human readout (e.g., "12 passing",
// "10 passing, 2 failing") linked to the appropriate host checks
// page when available.
func statusChecksRow(rollup code.CheckRollup, checksURL string) (string, string) {
	glyph := statusStyledGlyph(statusChecksGlyph(rollup.State))

	var v string
	if rollup.State == "none" {
		v = lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("(none)")
	} else {
		v = lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(checksSummary(rollup))
		if checksURL != "" {
			v = osc8Link(checksURL, v)
		}
	}
	return glyph, v
}

// statusChecksGlyph maps a checks rollup state onto the 3-cell glyph
// token. All states render as ● colored to severity — checks are an
// ongoing aspect (new commits can change the result), so the row is
// always state-context, never terminal.
//
// State-context call site — see state_grammar.go.
func statusChecksGlyph(state string) (string, color.Color) {
	switch state {
	case "passing":
		return ui.Palette.Active + "  ", ui.Palette.RoleOpen
	case "failing":
		return ui.Palette.Active + "  ", ui.Palette.RoleAttention
	case "running":
		return ui.Palette.Active + "  ", ui.Palette.RoleInFlight
	default: // "none" or unknown
		return ui.Palette.Active + "  ", ui.Palette.RoleNeutral
	}
}

// checksSummary composes the human readout ("12 passing" or
// "10 passing, 2 failing") from a CheckRollup. Suppresses zero
// buckets so the line stays as short as the data allows.
func checksSummary(rollup code.CheckRollup) string {
	var parts []string
	if rollup.Passing > 0 {
		parts = append(parts, fmt.Sprintf("%d passing", rollup.Passing))
	}
	if rollup.Failing > 0 {
		parts = append(parts, fmt.Sprintf("%d failing", rollup.Failing))
	}
	if rollup.Running > 0 {
		parts = append(parts, fmt.Sprintf("%d running", rollup.Running))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
}

// statusPreviewValue returns the value-only rendering of a preview
// binding for KV-style body rows (no glyph slot). Always returns
// something — "(none)" when no env, "(unverified)" suffix on
// indeterminate probe, "(unavailable)" on other errors. Used at
// workspace scope where the issue card body is KV-formatted and
// state nuance must live inside the value text.
func statusPreviewValue(env preview.Environment, err error) string {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	normal := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg)

	if errors.Is(err, preview.ErrNoEnvironment) {
		return muted.Render("(none)")
	}
	var probeErr *preview.ProbeError
	if errors.As(err, &probeErr) {
		if env.Name == "" {
			return muted.Render("(none)")
		}
		v := normal.Render(env.Name)
		if env.URL != "" {
			v = osc8Link(env.URL, v)
		}
		return v + " " + muted.Render("(unverified)")
	}
	if err != nil {
		return muted.Render("(unavailable)")
	}
	if env.Name == "" {
		return muted.Render("(none)")
	}
	v := normal.Render(env.Name)
	if env.URL != "" {
		v = osc8Link(env.URL, v)
	}
	if note := previewStateNote(env); note != "" {
		v += " " + muted.Render(note)
	}
	return v
}

// previewStateNote returns the parenthetical that qualifies a bound
// env's name, or "" when the env is serving and needs none.
//
// The distinctions come from the provider's status taxonomy. A
// reachability probe can only say active or gone, so it lands on
// "(unreachable)" — accurate but ambiguous, since a deploy triggered
// seconds ago and an env torn down last week both look identical to it.
// A provider that reads a deployment API separates them, and this is
// where that shows up.
func previewStateNote(env preview.Environment) string {
	if !env.Probed {
		return ""
	}
	switch env.Status {
	case preview.StatusCreating:
		return "(deploying)"
	case preview.StatusDeleting:
		return "(tearing down)"
	case preview.StatusDegraded:
		if n := len(env.FailedServices); n > 0 {
			return fmt.Sprintf("(degraded: %s)", strings.Join(env.FailedServices, ", "))
		}
		return "(degraded)"
	case preview.StatusGone:
		// Torn down, or a just-triggered deploy the provider can't see
		// yet. Surface the binding so it isn't lost, marked so the
		// reader knows it isn't reachable.
		return "(unreachable)"
	default:
		return ""
	}
}

// statusPreviewRow returns the (glyph, value) pair for a workspace
// card's Preview row, or ("", "") to signal "no row" (the caller
// decides whether to skip vs render "(none)" based on scope).
//
// Render shapes:
//   - active                        → ● RoleOpen, name (linked to URL)
//   - degraded                      → ● RoleOpen, name (linked) + (degraded: svc, …) suffix
//   - indeterminate (ProbeError)    → ● RoleNeutral, name (linked) + (unverified) suffix
//   - creating / deleting           → ● RoleNeutral, name (linked) + transition suffix
//   - gone (probed dead)            → ● RoleNeutral, name (linked) + (unreachable) suffix
//   - unprobable (no URL template)  → ● RoleNeutral, name (no link)
//   - no env bound (ErrNoEnvironment or any other error) → ("", "") signaling skip
//
// Degraded reads as RoleOpen because the env is serving: it is a
// qualified success, not a neutral unknown, and the suffix carries which
// services didn't make it.
func statusPreviewRow(env preview.Environment, err error) (string, string) {
	if errors.Is(err, preview.ErrNoEnvironment) {
		return "", ""
	}

	// State-context call site — see state_grammar.go. All present-env
	// outcomes are current states (not events), so the row uses ●
	// colored to role: alive → RoleOpen, indeterminate / unprobable
	// → RoleNeutral.
	var probeErr *preview.ProbeError
	if errors.As(err, &probeErr) {
		if env.Name == "" {
			return "", ""
		}
		glyph := statusStyledGlyph(ui.Palette.Active+"  ", ui.Palette.RoleNeutral)
		v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(env.Name)
		if env.URL != "" {
			v = osc8Link(env.URL, v)
		}
		v += " " + lipgloss.NewStyle().Foreground(ui.Palette.RoleNeutral).Render("(unverified)")
		return glyph, v
	}

	if err != nil || env.Name == "" {
		return "", ""
	}

	role := ui.Palette.RoleNeutral
	if env.Alive() {
		role = ui.Palette.RoleOpen
	}
	glyph := statusStyledGlyph(ui.Palette.Active+"  ", role)
	v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(env.Name)
	if env.URL != "" {
		v = osc8Link(env.URL, v)
	}
	// An unprobed env (no URL template, or an unrecognized status) gets
	// no suffix: the name is what's known, and any qualifier would imply
	// a verification that never happened.
	if note := previewStateNote(env); note != "" {
		v += " " + lipgloss.NewStyle().Foreground(ui.Palette.RoleNeutral).Render(note)
	}
	return glyph, v
}

// statusStateGlyph returns the 3-cell glyph token for a card-state,
// matching the body-row glyph slot width used in workspace cards.
// Used at project scope for the Repos rollup body row (its glyph
// echoes the workspace's gutter state).
//
// State-context call site — see state_grammar.go. CardSuccess /
// CardFailed keep their event-shaped glyphs because at the rollup
// level they encode the same terminal-positive / terminal-negative
// resolution distinction (all-merged → ✓ RoleDone, all-broken →
// ✗ RoleClosed). The intermediate states (CardReady / CardSkipped
// / CardWaiting) collapse to ● colored to role.
func statusStateGlyph(state ui.CardState) (string, color.Color) {
	switch state {
	case ui.CardSuccess:
		return ui.Palette.Check + "  ", ui.Palette.RoleDone
	case ui.CardReady:
		return ui.Palette.Active + "  ", ui.Palette.RoleOpen
	case ui.CardSkipped:
		return ui.Palette.Active + "  ", ui.Palette.RoleAttention
	case ui.CardWaiting:
		return ui.Palette.Active + "  ", ui.Palette.RoleInFlight
	case ui.CardFailed:
		return ui.Palette.Cross + "  ", ui.Palette.RoleClosed
	}
	return ui.Palette.Active + "  ", ui.Palette.RoleNeutral
}

// lifecycleKeyGlyph maps a bosun lifecycle key (one of the keys in
// lifecycleStatusKeys, plus "done") onto a 3-cell glyph + color.
// Keyed on the canonical bosun lifecycle vocab rather than
// provider-specific workflow strings — callers do the reverse-lookup
// via lifecycleKeyForStatus first, so user overrides in the
// `statuses.*` config flow through naturally. Unknown keys (including
// "" when the status isn't mapped) fall back to in-flight.
//
// State-context call site — see state_grammar.go. Only "done"
// uses ✓ (terminal positive resolution, RoleDone/purple); every
// other lifecycle stage is an active state and renders as ● colored
// to its role.
func lifecycleKeyGlyph(key string) (string, color.Color) {
	switch key {
	case "done":
		return ui.Palette.Check + "  ", ui.Palette.RoleDone
	case "ready_for_release":
		return ui.Palette.Active + "  ", ui.Palette.RoleOpen
	case "blocked":
		return ui.Palette.Active + "  ", ui.Palette.RoleAttention
	}
	return ui.Palette.Active + "  ", ui.Palette.RoleInFlight
}

// statusUpdatedGlyph buckets a workspace's age-in-days into a
// freshness glyph for the Updated body row. Fresh (<7 days) reads
// as healthy; stale (7-30 days) needs attention; very stale (>30
// days) treats as closed (cleanup candidate).
//
// State-context call site — see state_grammar.go. Staleness is a
// state, not an event outcome, so the row uses ● colored to role.
func statusUpdatedGlyph(days int) (string, color.Color) {
	switch {
	case days >= 30:
		return ui.Palette.Active + "  ", ui.Palette.RoleClosed
	case days >= 7:
		return ui.Palette.Active + "  ", ui.Palette.RoleAttention
	default:
		return ui.Palette.Active + "  ", ui.Palette.RoleOpen
	}
}

// The workspace meta cards (Story, Workspace, Preview) all render as
// a colored ● dot — they're state indicators, not action results.
// The dot color carries the semantic; shape stays uniform across the
// block so the meta row reads as a scannable status strip. The
// per-aspect color helpers below feed Card.GlyphColor on a CardReady
// card to produce the dot.

// stalenessColor returns the dot color for the Workspace meta card
// (and for the age parenthetical in its value). Fresh reads as an
// active/healthy workspace, stale needs attention, very stale is a
// terminal "closed/abandoned" signal; an unknown timestamp renders
// as neutral.
//
// State-context call site — see state_grammar.go.
func stalenessColor(t time.Time) color.Color {
	if t.IsZero() {
		return ui.Palette.RoleNeutral
	}
	days := int(time.Since(t).Hours() / 24)
	switch {
	case days >= 30:
		return ui.Palette.RoleClosed
	case days >= 7:
		return ui.Palette.RoleAttention
	default:
		return ui.Palette.RoleOpen
	}
}

// lifecycleKeyDotColor returns the dot color for the Story meta
// card. Done / ready_for_release read as open-healthy (action
// available or finalized in tracker); blocked needs attention;
// everything else (in flight) reads as in-flight blue.
//
// Note that "done" maps to RoleOpen (green) on the dot, not RoleDone
// (purple). The "done" lifecycle stage shows as a green ● because
// the *card glyph itself* is a state-active marker; if we ever
// promote done to a terminal ✓, that's the place to use RoleDone.
//
// State-context call site — see state_grammar.go.
func lifecycleKeyDotColor(key string) color.Color {
	switch key {
	case "done", "ready_for_release":
		return ui.Palette.RoleOpen
	case "blocked":
		return ui.Palette.RoleAttention
	}
	return ui.Palette.RoleInFlight
}

// stepperSlotKeys is the fixed slot order of the workspace Status
// card's lifecycle stepper — lifecycleStatusKeys minus "acceptance"
// (excluded in v1; statuses mapped to it render the unavailable
// fallback) plus the terminal "done".
var stepperSlotKeys = []string{
	"ready",
	"in_progress",
	"blocked",
	"review",
	"preview",
	"ready_for_release",
	"done",
}

// stepperSlotWidth is the column span of one stepper slot: a 1-cell
// dot plus the 5-cell stepperConnector that follows it. Used to
// position the elbow + label under the active dot.
const stepperSlotWidth = 6

// stepperConnector is the run of rule between two stepper dots. Its
// width is baked into stepperSlotWidth — keep the two in step.
const stepperConnector = " " + ui.BoxHorizontal + ui.BoxHorizontal + ui.BoxHorizontal + " "

// stepperElbow points from the track down to the active slot's label.
const stepperElbow = ui.BoxCornerBL + ui.BoxHorizontal + " "

// stepperSlotIndex returns the slot position of a lifecycle key in
// the stepper track, or -1 when the key has no slot ("" for unmapped
// statuses, and "acceptance" — the documented v1 exclusion).
func stepperSlotIndex(key string) int {
	for i, k := range stepperSlotKeys {
		if k == key {
			return i
		}
	}
	return -1
}

// renderLifecycleStepper renders the 7-slot lifecycle stepper as a
// two-line string for Card.Value: the dot track on the first line,
// a colored elbow + status label pointing at the active slot on the
// second. The active dot, elbow, and label share the lifecycle role
// color (lifecycleKeyDotColor); inactive slots and connectors stay
// muted, so the single colored slot is the card's state signal — this
// row doubles as the color legend for the meta block. Blocked renders
// its active dot as ✗: the slot is a real column in the sprint-board
// model, but the work in it is negatively interrupted.
//
// Every segment is styled explicitly (no reliance on the Card value
// style) so embedded ANSI resets can't bleed the colors. Callers must
// pass a key with a stepper slot (stepperSlotIndex >= 0);
// buildWorkspaceStatusCard owns the unavailable fallback.
func renderLifecycleStepper(currentKey string) string {
	idx := stepperSlotIndex(currentKey)
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	activeStyle := lipgloss.NewStyle().Foreground(lifecycleKeyDotColor(currentKey))

	var track strings.Builder
	for i, key := range stepperSlotKeys {
		if i > 0 {
			track.WriteString(mutedStyle.Render(stepperConnector))
		}
		switch {
		case i == idx && key == "blocked":
			track.WriteString(activeStyle.Render(ui.Palette.Cross))
		case i == idx:
			track.WriteString(activeStyle.Render(ui.Palette.Active))
		default:
			track.WriteString(mutedStyle.Render(ui.Palette.Inactive))
		}
	}

	label, err := resolveStatus(currentKey)
	if err != nil {
		label = currentKey
	}
	elbow := strings.Repeat(" ", idx*stepperSlotWidth) + activeStyle.Render(stepperElbow+label)

	return track.String() + "\n" + elbow
}

// renderLifecycleStepperUnmapped renders the default stepper visual
// for a status the tracker returned but we don't have mapped to a
// lifecycle slot — every dot stays open ○ (none filled) and the
// elbow points at slot 0 with the raw status text as its label.
// Every segment renders muted, so the whole row reads as "we got
// something, we don't know where it sits."
//
// Distinct from buildWorkspaceStatusCard's !fetchOK collapsed row,
// which uses ▲ + "(unavailable)" to signal "we couldn't fetch at
// all" — the two cases now look different so the user can tell
// "tracker is down" from "tracker is fine, this status is novel."
func renderLifecycleStepperUnmapped(statusText string) string {
	mutedStyle := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

	var track strings.Builder
	for i := range stepperSlotKeys {
		if i > 0 {
			track.WriteString(mutedStyle.Render(stepperConnector))
		}
		track.WriteString(mutedStyle.Render(ui.Palette.Inactive))
	}

	label := statusText
	if label == "" {
		label = "(unknown)"
	}
	elbow := mutedStyle.Render(stepperElbow + label)

	return track.String() + "\n" + elbow
}

// humanizeAge formats a duration into a coarse "N unit ago" label
// suitable for the Updated body row. Buckets: <1m → "just now",
// <1h → "Nm ago", <1d → "Nh ago", <30d → "Nd ago", <365d →
// "Nmo ago", else "Ny ago". Units are abbreviated to keep the row
// narrow.
func humanizeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}

// statusUpdatedRow returns the (glyph, value) pair for a workspace
// card's Updated row at project scope, or ("", "") to signal "skip"
// when no timestamp was captured. Glyph encodes staleness bucket;
// value is a humanized relative time.
func statusUpdatedRow(t time.Time) (string, string) {
	if t.IsZero() {
		return "", ""
	}
	age := time.Since(t)
	days := int(age.Hours() / 24)
	glyph, c := statusUpdatedGlyph(days)
	v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(humanizeAge(age))
	return statusStyledGlyph(glyph, c), v
}

// statusUpdatedValue returns the value-only humanized age for KV-style
// body rows (no glyph slot). Returns "(unknown)" muted when t is zero.
func statusUpdatedValue(t time.Time) string {
	if t.IsZero() {
		return lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("(unknown)")
	}
	return lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(humanizeAge(time.Since(t)))
}

// projectRepoEntry is one repo in the project's Repos KV value.
type projectRepoEntry struct {
	name string
	url  string // optional host repo URL for click-through
}

// projectRepoColumns lays out repo names into columns that fit
// within `width` characters, with `gap` spaces between columns.
// Column-major ordering (entries read down each column, then across)
// — same as `ls` without `-l`. Prefers a vertical run of at least
// minRows lines before expanding to multiple columns. Each cell is
// NormalFg with OSC 8 link wrap when a URL is provided. Width
// measurement uses plain names; OSC 8 link wraps after padding so
// the click target is the text, not the trailing whitespace gap.
func projectRepoColumns(repos []projectRepoEntry, width, gap, minRows int) []string {
	if len(repos) == 0 {
		return nil
	}
	maxW := 0
	for _, r := range repos {
		if w := lipgloss.Width(r.name); w > maxW {
			maxW = w
		}
	}
	cols := 1
	if maxW+gap > 0 {
		cols = (width + gap) / (maxW + gap)
	}
	if cols < 1 {
		cols = 1
	}
	maxColsByRows := (len(repos) + minRows - 1) / minRows
	if cols > maxColsByRows {
		cols = maxColsByRows
	}
	if cols > len(repos) {
		cols = len(repos)
	}
	rows := (len(repos) + cols - 1) / cols
	normalFg := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg)

	out := make([]string, rows)
	for r := 0; r < rows; r++ {
		var parts []string
		for c := 0; c < cols; c++ {
			idx := c*rows + r // column-major
			if idx >= len(repos) {
				break
			}
			repo := repos[idx]
			pad := maxW - lipgloss.Width(repo.name)
			if pad < 0 {
				pad = 0
			}
			linked := normalFg.Render(repo.name)
			if repo.url != "" {
				linked = osc8Link(repo.url, linked)
			}
			parts = append(parts, linked+strings.Repeat(" ", pad))
		}
		out[r] = strings.Join(parts, strings.Repeat(" ", gap))
	}
	return out
}

// osc8Link wraps text in OSC 8 escape sequences so terminals that
// support hyperlinks render it as a clickable link to url. Terminals
// that don't support OSC 8 ignore the escapes and show the bare text.
func osc8Link(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
