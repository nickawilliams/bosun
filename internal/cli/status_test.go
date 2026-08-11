package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/code"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs"
	"github.com/nickawilliams/bosun/internal/workspace"
)

// containsAll fails the test naming the first wanted substring missing
// from out, with the rendered card attached for context.
func containsAll(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("card missing %q:\n%s", w, out)
		}
	}
}

// containsNone fails the test if any of the given substrings appears
// in out — the "section absent" half of the section assertions.
func containsNone(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(out, w) {
			t.Errorf("card unexpectedly contains %q:\n%s", w, out)
		}
	}
}

func TestBuildWorkspaceStoryCard(t *testing.T) {
	t.Run("fetch failed falls back to key only", func(t *testing.T) {
		card := buildWorkspaceStoryCard(issuepkg.Issue{
			Key:  "PROJ-42",
			Type: "Story",
		}, "PROJ-42", false)

		containsAll(t, ansi.Strip(card.Render()), "▲", "PROJ-42", "title unavailable")
	})

	t.Run("fetch succeeded renders key and title", func(t *testing.T) {
		card := buildWorkspaceStoryCard(issuepkg.Issue{
			Key:   "PROJ-99",
			Title: "Add dark mode",
			Type:  "Story",
		}, "PROJ-99", true)

		containsAll(t, ansi.Strip(card.Render()), "●", "PROJ-99", "Add dark mode")
	})
}

// TestBuildWorkspaceRepoCard covers the per-repo card `bosun status`
// renders at workspace scope. The E2E scenarios in status_e2e_test.go
// run under the raw reporter, where nothing is drawn, so section
// presence/absence is only assertable here.
func TestBuildWorkspaceRepoCard(t *testing.T) {
	repo := workspace.RepositoryStatus{Name: "api", Branch: "story/EX-1_feature"}

	t.Run("no host data renders empty PR and checks sections", func(t *testing.T) {
		// What an unconfigured (or failing) code host leaves behind: a
		// zero repoState. The rows still render — the branch is local
		// git — but PR and Checks read "(none)" rather than vanishing.
		card := buildWorkspaceRepoCard(repo, repoState{})

		out := ansi.Strip(card.Render())
		containsAll(t, out, "api", "Branch", "story/EX-1_feature", "Checks", "PR", "(none)")
		containsNone(t, out, "#")
	})

	t.Run("open PR renders number and dominant label", func(t *testing.T) {
		card := buildWorkspaceRepoCard(repo, repoState{
			sync: vcs.BranchSync{HasRemote: true},
			pr: code.PullRequest{
				Number: 12, State: "open", MergeableState: "clean", Review: "approved",
			},
		})

		// The sync state rides on the row glyph, not the value — the
		// value carries the branch name and the PR row the dominant
		// label folded out of state + mergeable + review.
		containsAll(t, ansi.Strip(card.Render()), "Branch", "story/EX-1_feature", "#12", "(approved)")
	})

	t.Run("dirty worktree marks the branch value", func(t *testing.T) {
		dirtyRepo := repo
		dirtyRepo.Dirty = true
		card := buildWorkspaceRepoCard(dirtyRepo, repoState{sync: vcs.BranchSync{HasRemote: true}})

		containsAll(t, ansi.Strip(card.Render()), "story/EX-1_feature*")
	})
}

// TestBuildProjectWorkspaceCard covers the per-workspace card `bosun
// status` renders at project scope, where the Preview row is dropped
// rather than shown as "(none)" — the one section whose presence is
// conditional.
func TestBuildProjectWorkspaceCard(t *testing.T) {
	base := workspaceState{
		name:     "story/EX-1_feature",
		issueKey: "EX-1",
		issue:    issuepkg.Issue{Key: "EX-1", Title: "Add feature", Status: "In Progress"},
		rollup:   ui.CardWaiting,
		counts:   workspaceRepoCounts{repos: 2, ready: 1, pending: 1},
	}

	t.Run("no preview env drops the preview row", func(t *testing.T) {
		ws := base
		ws.previewErr = preview.ErrNoEnvironment
		out := ansi.Strip(buildProjectWorkspaceCard(ws).Render())

		containsAll(t, out,
			"EX-1", "Add feature", "Status", "In Progress",
			"Repos", "2 repositories", "1 ready", "1 pending",
		)
		containsNone(t, out, "Preview")
	})

	t.Run("bound preview env adds the preview row", func(t *testing.T) {
		ws := base
		ws.previewEnv = preview.Environment{Name: "brave-falcon", Probed: true, Alive: true}
		out := ansi.Strip(buildProjectWorkspaceCard(ws).Render())

		containsAll(t, out, "Preview", "brave-falcon")
	})

	t.Run("unknown issue falls back to the workspace name", func(t *testing.T) {
		// Project scope derives the issue key from the workspace name
		// and swallows tracker failures, so a workspace whose issue
		// couldn't be fetched still needs an identity to render under.
		out := ansi.Strip(buildProjectWorkspaceCard(workspaceState{
			name:       "story/EX-1_feature",
			rollup:     ui.CardWaiting,
			counts:     workspaceRepoCounts{repos: 1, pending: 1},
			previewErr: preview.ErrNoEnvironment,
		}).Render())

		containsAll(t, out, "story/EX-1_feature", "1 repository")
		containsNone(t, out, "Status")
	})
}
