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

// statusCardStateColor returns the palette color associated with a
// resolved aggregate card state — the same hue used by the gutter
// glyph — so a card's title can be tinted to match.
func statusCardStateColor(state ui.CardState) color.Color {
	switch state {
	case ui.CardSuccess:
		return ui.Palette.Success
	case ui.CardReady:
		return ui.Palette.Success
	case ui.CardSkipped:
		return ui.Palette.Warning
	case ui.CardWaiting:
		return ui.Palette.Info
	case ui.CardFailed:
		return ui.Palette.Error
	}
	return ui.Palette.Primary
}

// resolveRepoCardState maps a repo's branch + PR state onto the
// 5-state aggregate vocabulary used at the gutter level. Mirrors the
// scratchpad's resolveWstatRepoCardState. Precedence: terminal PR
// states (merged → done, closed → broken) win; then branch problems
// (diverged / behind) → blocked; then PR mergeability (clean → ready,
// dirty/behind/blocked/unstable → blocked); else pending.
func resolveRepoCardState(branchState string, pr code.PullRequest) ui.CardState {
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
		case "dirty", "behind", "blocked", "unstable":
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
func statusBranchGlyph(sync vcs.BranchSync) (string, color.Color) {
	if !sync.HasRemote {
		// + means "additions" — N local commits ahead of base, no
		// remote yet. Pairs cleanly with digits and reads directly
		// as "N to push."
		return "+" + countToken(sync.Ahead), ui.Palette.Muted
	}
	switch {
	case sync.Ahead == 0 && sync.Behind == 0:
		return "✓  ", ui.Palette.Success
	case sync.Ahead > 0 && sync.Behind > 0:
		// Sum of ahead + behind — magnitude of divergence, not its
		// split. The glyph says "two-way", the number says "this
		// much to reconcile."
		return "↕" + countToken(sync.Ahead+sync.Behind), ui.Palette.Error
	case sync.Ahead > 0:
		return "↑" + countToken(sync.Ahead), ui.Palette.Info
	default:
		return "↓" + countToken(sync.Behind), ui.Palette.Warning
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
// branch name (linked via OSC 8 to its GitHub URL when known) with
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
func statusPRRow(pr code.PullRequest) (string, string) {
	if pr.Number == 0 {
		// No PR exists — muted "(none)".
		glyph := statusStyledGlyph(statusPRGlyph("", ""))
		return glyph, lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("(none)")
	}

	glyph := statusStyledGlyph(statusPRGlyph(pr.State, pr.MergeableState))

	number := fmt.Sprintf("#%d", pr.Number)
	v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(number)
	if pr.URL != "" {
		v = osc8Link(pr.URL, v)
	}
	if label := statusPRStatusLabel(pr.State, pr.Review); label != "" {
		styled := lipgloss.NewStyle().
			Foreground(statusPRStatusColor(pr.State, pr.Review)).
			Render("(" + label + ")")
		v += " " + styled
	}
	return glyph, v
}

// statusPRGlyph returns the 3-cell glyph token for the PR row.
// Aligns with the 5-state vocab (✓ ● ▲ ⧗ ✗) with a single meta-state
// exception: `?` for open + unknown mergeable_state.
func statusPRGlyph(state, mergeState string) (string, color.Color) {
	switch state {
	case "merged":
		return "✓  ", ui.Palette.Success
	case "open":
		switch mergeState {
		case "clean":
			return "●  ", ui.Palette.Success
		case "dirty", "behind", "unstable", "blocked":
			return "▲  ", ui.Palette.Warning
		case "unknown":
			return "?  ", ui.Palette.Muted
		}
		return "⧗  ", ui.Palette.Info
	case "draft":
		return "⧗  ", ui.Palette.Info
	case "closed":
		return "✗  ", ui.Palette.Error
	default: // "" (no PR)
		return "⧗  ", ui.Palette.Info
	}
}

// statusPRStatusLabel returns the display state for the parenthetical
// after the PR number. Folds review state into "open" as
// subdivisions: open + approved → "approved", + changes_requested →
// "changes requested", else bare "open".
func statusPRStatusLabel(state, reviewState string) string {
	switch state {
	case "merged", "draft", "closed":
		return state
	case "open":
		switch reviewState {
		case "approved":
			return "approved"
		case "changes_requested":
			return "changes requested"
		}
		return "open"
	}
	return ""
}

// statusPRStatusColor returns the palette color for the unified PR
// display state, echoing GitHub's badge palette.
func statusPRStatusColor(state, reviewState string) color.Color {
	switch state {
	case "merged":
		return ui.Palette.Primary // indigo, our purple-family
	case "open":
		switch reviewState {
		case "approved":
			return ui.Palette.Success
		case "changes_requested":
			return ui.Palette.Warning
		}
		return ui.Palette.Info
	case "draft":
		return ui.Palette.Muted
	case "closed":
		return ui.Palette.Error
	}
	return ui.Palette.Muted
}

// statusChecksRow returns the (glyph, value) pair for a repo card's
// Checks row. Value is the human readout (e.g., "12 passing",
// "10 passing, 2 failing") linked to the appropriate GitHub checks
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
// token. Aligns with the 5-state vocab — passing → ✓, failing → ▲
// (blocked), running / none → ⧗ (pending).
func statusChecksGlyph(state string) (string, color.Color) {
	switch state {
	case "passing":
		return "✓  ", ui.Palette.Success
	case "failing":
		return "▲  ", ui.Palette.Warning
	case "running":
		return "⧗  ", ui.Palette.Info
	default: // "none" or unknown
		return "⧗  ", ui.Palette.Info
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
	return v
}

// statusPreviewRow returns the (glyph, value) pair for a workspace
// card's Preview row, or ("", "") to signal "no row" (the caller
// decides whether to skip vs render "(none)" based on scope).
//
// Render shapes:
//   - alive (probed + alive)        → ✓ Success, name (linked to URL)
//   - indeterminate (ProbeError)    → ? Muted, name (linked) + (unverified) muted suffix
//   - unprobable (no URL template)  → ◦ Muted, name (no link)
//   - bound but probed dead         → handled by adapter auto-clear; surfaces as no-env
//   - no env bound (ErrNoEnvironment or any other error) → ("", "") signaling skip
func statusPreviewRow(env preview.Environment, err error) (string, string) {
	if errors.Is(err, preview.ErrNoEnvironment) {
		return "", ""
	}

	var probeErr *preview.ProbeError
	if errors.As(err, &probeErr) {
		if env.Name == "" {
			return "", ""
		}
		glyph := statusStyledGlyph("?  ", ui.Palette.Muted)
		v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(env.Name)
		if env.URL != "" {
			v = osc8Link(env.URL, v)
		}
		v += " " + lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("(unverified)")
		return glyph, v
	}

	if err != nil || env.Name == "" {
		return "", ""
	}

	switch {
	case env.Probed && env.Alive:
		glyph := statusStyledGlyph("✓  ", ui.Palette.Success)
		v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(env.Name)
		if env.URL != "" {
			v = osc8Link(env.URL, v)
		}
		return glyph, v
	default:
		// Unprobable (no URL template): show the name muted so the
		// reader can see what's bound without implying it's verified.
		glyph := statusStyledGlyph("◦  ", ui.Palette.Muted)
		v := lipgloss.NewStyle().Foreground(ui.Palette.NormalFg).Render(env.Name)
		if env.URL != "" {
			v = osc8Link(env.URL, v)
		}
		return glyph, v
	}
}

// statusStateGlyph returns the 3-cell glyph token for a card-state,
// matching the body-row glyph slot width used in workspace cards.
// Used at project scope for the Repos rollup body row (its glyph
// echoes the workspace's gutter state).
func statusStateGlyph(state ui.CardState) (string, color.Color) {
	switch state {
	case ui.CardSuccess:
		return "✓  ", ui.Palette.Success
	case ui.CardReady:
		return "●  ", ui.Palette.Success
	case ui.CardSkipped:
		return "▲  ", ui.Palette.Warning
	case ui.CardWaiting:
		return "⧗  ", ui.Palette.Info
	case ui.CardFailed:
		return "✗  ", ui.Palette.Error
	}
	return "◦  ", ui.Palette.Muted
}

// lifecycleKeyGlyph maps a bosun lifecycle key (one of the keys in
// lifecycleStatusKeys, plus "done") onto the 5-state vocab for the
// Status body row. Keyed on the canonical bosun lifecycle vocab
// rather than provider-specific workflow strings — callers do the
// reverse-lookup via lifecycleKeyForStatus first, so user overrides
// in the `statuses.*` config flow through naturally. Unknown keys
// (including "" when the status isn't mapped) fall back to pending.
func lifecycleKeyGlyph(key string) (string, color.Color) {
	switch key {
	case "done":
		return "✓  ", ui.Palette.Success
	case "ready_for_release":
		return "●  ", ui.Palette.Success
	case "blocked":
		return "▲  ", ui.Palette.Warning
	}
	return "⧗  ", ui.Palette.Info
}

// statusUpdatedGlyph buckets a workspace's age-in-days into a
// freshness glyph for the Updated body row. Fresh (<7 days) reads
// as muted; stale (7-30 days) warns; very stale (>30 days) treats
// as broken (cleanup candidate).
func statusUpdatedGlyph(days int) (string, color.Color) {
	switch {
	case days >= 30:
		return "✗  ", ui.Palette.Error
	case days >= 7:
		return "▲  ", ui.Palette.Warning
	default:
		return "◦  ", ui.Palette.Muted
	}
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
	url  string // optional GitHub repo URL for click-through
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
