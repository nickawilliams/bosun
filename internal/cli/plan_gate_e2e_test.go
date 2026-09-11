package cli_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/testharness"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;:]*[a-zA-Z]`)

// planRowMarkers are the subject-type column values a cleanup plan
// row carries. Their presence directly under the gate's card is what
// distinguishes the two mount shapes.
var planRowMarkers = []string{"worktree", "branch", "directory"}

// gateCardRows returns the plan rows rendered inside the gate's
// Pending card — the lines between its title row and the end of the
// card. An empty result means the card mounted compact, with its
// rows committed to scrollback above instead.
func gateCardRows(t *testing.T, out string) []string {
	t.Helper()

	lines := strings.Split(ansiEscape.ReplaceAllString(out, ""), "\n")
	title := -1
	for i, l := range lines {
		if strings.Contains(l, "Pending ·") {
			if title >= 0 {
				t.Fatalf("more than one Pending card in the gate output:\n%s", out)
			}
			title = i
		}
	}
	if title < 0 {
		t.Fatalf("no Pending card in the gate output — did the gate run?\n%s", out)
	}

	var rows []string
	for _, l := range lines[title+1:] {
		if !strings.ContainsAny(l, "-~+") {
			break // the card's rows ended
		}
		for _, m := range planRowMarkers {
			if strings.Contains(l, m) {
				rows = append(rows, strings.TrimSpace(l))
				break
			}
		}
	}
	return rows
}

// TestPlanGateMountShape pins which of runPlanCard's two gate mounts
// a plan gets, since the choice is invisible until it goes wrong: a
// plan whose card fits on screen is the prompt itself, rows and all,
// so the whole card can cycle in place; a plan taller than the
// screen would paint a frame bubbletea's inline renderer truncates
// (#69/#98), so its rows commit to scrollback first and only a
// compact card cycles.
//
// Both arms answer the readiness warning and then the gate, so the
// command runs through to its apply either way.
func TestPlanGateMountShape(t *testing.T) {
	t.Run("plan_fits/rows_ride_inside_the_gate_card", func(t *testing.T) {
		// One repo — a three-row plan, far inside the bound.
		h, _ := startCleanupWorkspace(t, "api")
		h.Type("y") // readiness warning
		h.Type("y") // plan gate

		if err := runCleanup(h); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		if rows := gateCardRows(t, h.Stdout()); len(rows) == 0 {
			t.Errorf("a plan that fits must carry its rows in the gate card, so the card can cycle in place; got a compact card:\n%s",
				h.Stdout())
		}
	})

	t.Run("plan_oversized/rows_commit_before_the_gate", func(t *testing.T) {
		// Nine repos — two rows each plus the workspace directory is
		// 19 plan rows, past what the test terminal's 24 rows can
		// hold once the confirm's own chrome is added.
		h, _ := startCleanupWorkspace(t,
			"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9")
		h.Type("y") // readiness warning
		h.Type("y") // plan gate

		if err := runCleanup(h); err != nil {
			t.Fatalf("cleanup: %v", err)
		}

		out := h.Stdout()
		if rows := gateCardRows(t, out); len(rows) > 0 {
			t.Errorf("an oversized plan must not paint its rows inside the live gate frame; found %d row(s) in the card:\n%s",
				len(rows), out)
		}
		// The rows aren't lost — they were committed above the gate.
		if !strings.Contains(ansiEscape.ReplaceAllString(out, ""), "directory") {
			t.Errorf("oversized plan dropped its rows entirely; they must commit to scrollback:\n%s", out)
		}
	})
}

// TestDemoRendersStaticSections is the design-reference command's
// smoke test: `bosun demo` with no flags walks every static section,
// which is 700-odd lines of rendering nothing else exercises.
//
// Cards don't reach stdout under the test reporter (Print is a no-op
// in raw mode), so the sections that assert here are the ones whose
// output is a form snapshot: the plan gate's confirm and the static
// form demo's. Counting them is what makes the gate's snapshot
// distinguishable — both render the same Approve/Cancel pair, so
// only the count says whether the gate section still emits one.
//
// The interactive sections (spinners, groups, forms, the gather
// picker) need a live TTY and stay outside this test.
func TestDemoRendersStaticSections(t *testing.T) {
	h := testharness.New(t)
	// demo is not project-scoped, so it rejects the --project flag
	// the harness injects for an initialized workspace.
	h.Workspace.Uninitialize()

	if err := h.Run("demo"); err != nil {
		t.Fatalf("demo: %v", err)
	}

	out := ansiEscape.ReplaceAllString(h.Stdout(), "")

	// A confirm's button row carries both labels; its help line
	// mentions them too ("y Approve • n Cancel"), so exclude that.
	buttons := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Approve") && strings.Contains(l, "Cancel") &&
			!strings.Contains(l, "toggle") {
			buttons++
		}
	}
	// One from demoPlanCardStates, one from demoFormStatic.
	if buttons != 2 {
		t.Errorf("demo rendered %d confirm button rows, want 2 (the plan gate's and the static form's):\n%s",
			buttons, out)
	}

	// A couple of other static sections, so a section dropped from
	// the walk is caught rather than silently skipped.
	for _, marker := range []string{"base_url", "Services"} {
		if !strings.Contains(out, marker) {
			t.Errorf("demo output is missing %q — a static section stopped rendering:\n%s", marker, out)
		}
	}
}
