package cli

// In-package coverage for `bosun review`'s target rows and record card.
// The E2E scenarios in review_e2e_test.go run under the raw reporter,
// which draws no cards at all — what a row says, and which state the
// card aggregates to, is only assertable from here.

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/nickawilliams/bosun/internal/code"
)

// target builds a repoContext for a named repo with the given PR state.
// A zero number means "no PR".
func target(name, state string, number int) repoContext {
	return repoContext{
		repo: Repository{Name: name},
		pr:   code.PullRequest{Number: number, State: state},
	}
}

// cardGlyph returns the leading state glyph of a rendered card.
func cardGlyph(t *testing.T, out string) string {
	t.Helper()
	fields := strings.Fields(ansi.Strip(out))
	if len(fields) == 0 {
		t.Fatalf("card rendered empty")
	}
	return fields[0]
}

func TestReviewTargetRow(t *testing.T) {
	t.Run("included repo with no PR renders the name alone", func(t *testing.T) {
		rc := target("api", "", 0)
		glyph, content := reviewTargetRow(&rc, true)

		if got := ansi.Strip(glyph); got != "✓" {
			t.Errorf("glyph = %q, want %q", got, "✓")
		}
		if got := ansi.Strip(content); got != "api" {
			t.Errorf("content = %q, want the bare repo name (no trailing separator)", got)
		}
	})

	t.Run("included repo with a PR appends its status note", func(t *testing.T) {
		rc := target("api", "open", 7)
		glyph, content := reviewTargetRow(&rc, true)

		if got := ansi.Strip(glyph); got != "✓" {
			t.Errorf("glyph = %q, want %q", got, "✓")
		}
		if got := ansi.Strip(content); got != "api · #7 (open)" {
			t.Errorf("content = %q, want the repo plus its PR status", got)
		}
	})

	t.Run("skipped repo recedes but keeps its note", func(t *testing.T) {
		rc := target("web", "merged", 3)
		glyph, content := reviewTargetRow(&rc, false)

		if got := ansi.Strip(glyph); got != "○" {
			t.Errorf("glyph = %q, want %q", got, "○")
		}
		if got := ansi.Strip(content); got != "web · #3 (merged)" {
			t.Errorf("content = %q, want the repo plus its PR status", got)
		}
	})

	t.Run("lookup failure wins over inclusion and names the error", func(t *testing.T) {
		rc := target("docs", "", 0)
		rc.prErr = errNope
		// on=true: a failed lookup must still render as a failure row.
		glyph, content := reviewTargetRow(&rc, true)

		if got := ansi.Strip(glyph); got != "✗" {
			t.Errorf("glyph = %q, want %q", got, "✗")
		}
		if got := ansi.Strip(content); got != "docs · lookup failed" {
			t.Errorf("content = %q, want the repo plus the error", got)
		}
	})
}

func TestBuildReviewTargetsCard(t *testing.T) {
	t.Run("rows are ordered by repo name", func(t *testing.T) {
		api := target("api", "", 0)
		api.include = true
		web := target("web", "", 0)
		web.include = true

		out := ansi.Strip(buildReviewTargetsCard([]repoContext{web, api}).Render())
		if strings.Index(out, "api") > strings.Index(out, "web") {
			t.Errorf("rows are not name-ordered:\n%s", out)
		}
	})

	t.Run("all included aggregates to success", func(t *testing.T) {
		api := target("api", "", 0)
		api.include = true

		out := buildReviewTargetsCard([]repoContext{api}).Render()
		if got := cardGlyph(t, out); got != "✓" {
			t.Errorf("card glyph = %q, want the success %q", got, "✓")
		}
	})

	t.Run("nothing included aggregates to skipped", func(t *testing.T) {
		// Every repo left out — no work to record, so the card reads as
		// skipped rather than a success with nothing in it.
		out := buildReviewTargetsCard([]repoContext{
			target("api", "open", 7),
			target("web", "merged", 3),
		}).Render()
		if got := cardGlyph(t, out); got != "▲" {
			t.Errorf("card glyph = %q, want the skipped %q", got, "▲")
		}
	})

	t.Run("a mix of included and skipped stays success", func(t *testing.T) {
		api := target("api", "", 0)
		api.include = true

		out := buildReviewTargetsCard([]repoContext{api, target("web", "open", 3)}).Render()
		if got := cardGlyph(t, out); got != "✓" {
			t.Errorf("card glyph = %q, want the success %q", got, "✓")
		}
	})

	t.Run("any failure dominates the aggregate", func(t *testing.T) {
		api := target("api", "", 0)
		api.include = true
		docs := target("docs", "", 0)
		docs.prErr = errNope

		out := buildReviewTargetsCard([]repoContext{api, docs}).Render()
		if got := cardGlyph(t, out); got != "✗" {
			t.Errorf("card glyph = %q, want the failure %q", got, "✗")
		}
	})
}
