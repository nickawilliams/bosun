package cli

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// mixedBasePlaceholder describes the "each repo targets its own default
// branch" answer in a prompt that otherwise shows a literal branch. The
// parentheses mark it as a rule rather than a branch you could type.
const mixedBasePlaceholder = "(per repo default)"

// basePlaceholder is what the shared Base Branch prompt offers as the
// no-op answer: the global override when one is set, the one default
// branch when every repo agrees on it, else a note that the base is
// resolved per repo. Submitting it changes nothing either way — the
// distinction matters only for what the prompt (and its record) shows.
func basePlaceholder(globalBase string, resolved []repoContext) string {
	if globalBase != "" {
		return globalBase
	}
	shared := ""
	for _, i := range writableRepos(resolved) {
		b := resolved[i].baseBranch()
		if shared == "" {
			shared = b
			continue
		}
		if b != shared {
			return mixedBasePlaceholder
		}
	}
	if shared == "" {
		return defaultBaseBranch
	}
	return shared
}

// writableRepos returns the indices of the repos this run will actually
// create or update a PR in, repo-alphabetically. Everything that asks
// "what do the repos look like?" — the base placeholder, the metadata
// preview, the customization pass — means these repos: a repo we're
// skipping (or whose lookup failed) shouldn't get a vote on prompts
// whose answers it will never consume.
func writableRepos(resolved []repoContext) []int {
	var idxs []int
	for i := range resolved {
		if resolved[i].include && resolved[i].prErr == nil {
			idxs = append(idxs, i)
		}
	}
	sort.SliceStable(idxs, func(a, b int) bool {
		return resolved[idxs[a]].repo.Name < resolved[idxs[b]].repo.Name
	})
	return idxs
}

// customizeRepoMetadata runs the opt-in per-repository override pass:
// a multi-select of the repos getting a PR (nothing checked by default),
// then one mini-form per checked repo pre-filled with that repo's
// current metadata, then a record card naming what diverged.
//
// Shared-by-default is the whole point — the common case is a uniform
// workspace where the shared answers are right for every repo, so the
// pass costs one Enter to decline. It exists for the workspaces where
// repos genuinely diverge: a different base branch, a different reviewer
// set (each repo's collaborators are fetched from that repo, not from
// whichever repo happened to sort first), a repo-specific title.
//
// Mutates resolved[i].meta in place. Returns ErrCancelled if the user
// aborts any form.
func customizeRepoMetadata(ctx context.Context, cmd *cobra.Command, host code.Host, resolved []repoContext, selfUser string) error {
	if host == nil || !promptForValues(cmd) {
		return nil
	}

	// Only repos we'll actually write to can diverge in a way that
	// matters, and with fewer than two of them the shared prompts WERE
	// the per-repo prompts — offering to customize would be asking the
	// same question twice.
	idxs := writableRepos(resolved)
	if len(idxs) < 2 {
		return nil
	}

	picked, err := pickCustomizeTargets(resolved, idxs)
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		return nil
	}

	for _, i := range picked {
		if err := editRepoMetadata(ctx, host, &resolved[i], selfUser); err != nil {
			return err
		}
	}

	buildCustomizeCard(resolved, picked).Print()
	return nil
}

// pickCustomizeTargets shows the "which repos?" multi-select over idxs
// with nothing checked, and returns the chosen indices into resolved
// (in idxs order). The rows carry each repo's current base so the
// mixed-base case — the reason this pass exists — is visible before the
// user commits to opening any form.
func pickCustomizeTargets(resolved []repoContext, idxs []int) ([]int, error) {
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	opts := make([]huh.Option[string], len(idxs))
	for j, i := range idxs {
		rc := &resolved[i]
		// Bold the repo segment with raw SGR 1/22 (not lipgloss, which
		// would close with a full reset) so huh's own selection styling
		// for the rest of the row survives — same trick as the
		// PR-selection form.
		label := "\x1b[1m" + rc.repo.Name + "\x1b[22m" + muted.Render(" · "+rc.meta.base)
		opts[j] = huh.NewOption(label, strconv.Itoa(i))
	}

	var chosen []string
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, "customize").Tight())
	if err := runForm(
		huh.NewMultiSelect[string]().
			Options(opts...).
			Height(min(len(opts), maxSelectHeight)).
			Value(&chosen),
	); err != nil {
		return nil, err
	}
	slot.Clear()

	// Preserve idxs order rather than selection order, so the per-repo
	// forms and the record card both run repo-alphabetically.
	want := make(map[int]bool, len(chosen))
	for _, c := range chosen {
		if i, err := strconv.Atoi(c); err == nil {
			want[i] = true
		}
	}
	var out []int
	var names []string
	for _, i := range idxs {
		if want[i] {
			out = append(out, i)
			names = append(names, resolved[i].repo.Name)
		}
	}
	ui.SelectedMulti("Customize", names)
	return out, nil
}

// editRepoMetadata runs one repo's mini-form: base, title, body, and —
// when the host can enumerate them — reviewers, team reviewers, and
// assignees, each pre-filled with the repo's current values. The option
// lists come from THIS repo (and its org), which is the divergence the
// shared prompts couldn't represent.
func editRepoMetadata(ctx context.Context, host code.Host, rc *repoContext, selfUser string) error {
	slot := ui.NewSlot()

	var collaborators, teams []string
	var fetchNotes []string
	// Neither list is required: a repo whose collaborators we can't read
	// still gets base/title/body fields and keeps the shared reviewer
	// sets. Failing the whole run over an optional picker would be worse
	// than the narrower form — but the failure is NOT silent, or the
	// user can't tell "no collaborators" from "token can't read them".
	// The closure therefore never returns an error; slot.Run's own error
	// path stays unreachable by construction.
	_ = slot.Run("fetching "+rc.repo.Name, func() error {
		if c, err := host.ListCollaborators(ctx, rc.owner, rc.repoName); err != nil {
			fetchNotes = append(fetchNotes,
				fmt.Sprintf("%s: collaborators: %v", rc.repo.Name, err))
		} else {
			collaborators = c
		}
		if t, err := host.ListTeams(ctx, rc.owner); err != nil {
			fetchNotes = append(fetchNotes,
				fmt.Sprintf("%s: teams: %v", rc.repo.Name, err))
		} else {
			teams = t
		}
		return nil
	})
	slot.Clear()
	for _, note := range fetchNotes {
		ui.Skip(note)
	}

	base, title := rc.meta.base, rc.meta.title
	body := rc.meta.body
	fields := []huh.Field{
		newInput("Base Branch").Value(&base),
		newInput("Title").Value(&title),
		newText("Body").Value(&body),
	}

	// Reviewers exclude the author (GitHub rejects a self-review with a
	// 422); assignees float them to the top instead.
	revOpts := withoutUser(collaborators, selfUser)
	reviewers := withoutUser(rc.meta.reviewers, selfUser)
	asnOpts := collaborators
	if selfUser != "" {
		asnOpts = userToFront(collaborators, selfUser)
	}
	teamSel := slices.Clone(rc.meta.teams)
	assignees := slices.Clone(rc.meta.assignees)

	// A multi-select with no options can't be submitted meaningfully, so
	// the field is dropped and the repo keeps its current values.
	revField := multiSelectOver(revOpts, &reviewers, "Reviewers", "")
	teamField := multiSelectOver(teams, &teamSel, "Team Reviewers", "")
	asnField := multiSelectOver(asnOpts, &assignees, "Assignees", selfUser)
	for _, f := range []huh.Field{revField, teamField, asnField} {
		if f != nil {
			fields = append(fields, f)
		}
	}

	slot.Show(ui.NewCard(ui.CardInput, ui.PreserveCase(rc.repo.Name)).Tight())
	if err := runForm(fields...); err != nil {
		return err
	}
	slot.Clear()

	// A cleared field means "I didn't want to change this", not "send an
	// empty value" — an empty base or title is a guaranteed 422 at Apply
	// time, after the plan was already approved. Each falls back to what
	// the field was pre-filled with, so a cleared base keeps any global
	// override rather than silently dropping to the repo default. Body
	// is genuinely allowed to be empty (most templates render nothing).
	rc.meta.base = firstNonBlank(base, rc.meta.base, rc.baseBranch())
	rc.meta.title = firstNonBlank(title, rc.meta.title)
	rc.meta.body = body
	if revField != nil {
		rc.meta.reviewers = reviewers
	}
	if teamField != nil {
		rc.meta.teams = teamSel
	}
	if asnField != nil {
		rc.meta.assignees = assignees
	}
	return nil
}

// firstNonBlank returns the first argument that isn't empty or
// whitespace, trimmed; "" when every candidate is blank.
func firstNonBlank(candidates ...string) string {
	for _, c := range candidates {
		if t := strings.TrimSpace(c); t != "" {
			return t
		}
	}
	return ""
}

// multiSelectOver builds a titled multi-select over options with the
// values already in sel pre-checked, or nil when there's nothing to
// choose from. Values in sel that aren't in options are folded in so a
// pre-filled answer is never silently dropped just because it came from
// config or another repo's collaborator list. promote, when set, is
// labeled "<user> (me)".
func multiSelectOver(options []string, sel *[]string, title, promote string) *huh.MultiSelect[string] {
	items := slices.Clone(options)
	for _, v := range *sel {
		if !slices.ContainsFunc(items, func(s string) bool { return strings.EqualFold(s, v) }) {
			items = append(items, v)
		}
	}
	if len(items) == 0 {
		return nil
	}

	checked := make(map[string]bool, len(*sel))
	for _, v := range *sel {
		checked[strings.ToLower(v)] = true
	}
	opts := make([]huh.Option[string], len(items))
	for i, item := range items {
		label := item
		if promote != "" && strings.EqualFold(item, promote) {
			label = item + " (me)"
		}
		opts[i] = huh.NewOption(label, item).Selected(checked[strings.ToLower(item)])
	}
	return huh.NewMultiSelect[string]().
		Title(transformFieldTitle(title)).
		Options(opts...).
		Height(min(len(opts), maxSelectHeight)).
		Value(sel)
}

// buildCustomizeCard records the override pass: one row per customized
// repo naming its base plus a count of the reviewer/assignee sets, so
// the timeline shows what each repo ended up with without replaying
// every field. The plan below is still the source of truth for what
// gets written.
func buildCustomizeCard(resolved []repoContext, picked []int) *ui.Card {
	primary := lipgloss.NewStyle().Foreground(ui.Palette.Primary)
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	check := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)

	card := ui.NewCard(ui.CardSuccess, "customize")
	for _, i := range picked {
		rc := &resolved[i]
		parts := []string{rc.meta.base}
		if n := len(rc.meta.reviewers) + len(rc.meta.teams); n > 0 {
			parts = append(parts, fmt.Sprintf("%d rev", n))
		}
		if n := len(rc.meta.assignees); n > 0 {
			parts = append(parts, fmt.Sprintf("%d asn", n))
		}
		card.Item(check, primary.Render(rc.repo.Name)+
			muted.Render(" · "+strings.Join(parts, " · ")))
	}
	return card
}
