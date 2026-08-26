package cli

import (
	"errors"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

func newDemoCmd() *cobra.Command {
	var interactive bool

	cmd := &cobra.Command{
		Use:    "demo",
		Short:  "Render all UI components for design iteration",
		Hidden: true,
		Annotations: map[string]string{
			headerAnnotationTitle: "UI components",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive {
				demoPalette()
				demoCards(cmd)
				demoSummary()
				demoTree()
				demoGroupsStatic()
				demoItemCard()
				demoWorkspaceMeta()
				demoPlanCardStates()
				demoFormStatic()
				return nil
			}

			if err := demoContinue("Spinners", true); err != nil {
				return err
			}
			demoSpinners()

			if err := demoContinue("Groups", false); err != nil {
				return err
			}
			demoGroups()

			if err := demoContinue("Forms", false); err != nil {
				return err
			}
			if err := demoForms(); err != nil {
				return err
			}

			if err := demoContinue("Slot", false); err != nil {
				return err
			}
			demoSlot()

			if err := demoContinue("Plan Card", false); err != nil {
				return err
			}
			demoPlanApply(cmd)
			demoPlanGated(cmd)
			demoPlanDryRun(cmd)
			demoPlanNoWork(cmd)

			if err := demoContinue("Summary", false); err != nil {
				return err
			}
			if err := demoSummaryInteractive(); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&interactive, "interactive", false,
		"include live spinners, forms, and animated elements")

	return cmd
}

// buildDemoPlan constructs a plan with all four operation types.
func buildDemoPlan() *ui.Plan {
	return ui.NewPlan().
		Add(ui.PlanCreate, "branch", "repo", "api", "feature/ABC-123").
		Add(ui.PlanCreate, "worktree", "repo", "api", "workspaces/ABC-123/api").
		Add(ui.PlanModify, "status", "issue", "ABC-123", "Open → In Progress").
		Add(ui.PlanDestroy, "branch", "repo", "web", "feature/OLD-456").
		Add(ui.PlanNoChange, "branch", "repo", "infra", "feature/ABC-123")
}

// --- Static sections ---

// demoPalette renders the active color palette as swatch rows, grouped
// the way the palette struct groups them (semantic / resolution-role /
// chrome), plus the symbol set. The canonical reference for "which
// color is which" when designing new components.
func demoPalette() {
	swatch := func(name string, col color.Color) string {
		block := lipgloss.NewStyle().Foreground(col).Render("██")
		return fmt.Sprintf("%s %-13s", block, name)
	}
	rows := func(entries ...string) []string {
		const perRow = 4
		var out []string
		for i := 0; i < len(entries); i += perRow {
			end := min(i+perRow, len(entries))
			out = append(out, strings.TrimRight(strings.Join(entries[i:end], " "), " "))
		}
		return out
	}
	header := lipgloss.NewStyle().Foreground(ui.Palette.Muted)

	var body []string
	body = append(body, header.Render("Semantic"))
	body = append(body, rows(
		swatch("Primary", ui.Palette.Primary),
		swatch("Secondary", ui.Palette.Secondary),
		swatch("Brand", ui.Palette.Brand),
		swatch("Accent", ui.Palette.Accent),
		swatch("Info", ui.Palette.Info),
		swatch("Success", ui.Palette.Success),
		swatch("Error", ui.Palette.Error),
		swatch("Warning", ui.Palette.Warning),
		swatch("Muted", ui.Palette.Muted),
		swatch("NormalFg", ui.Palette.NormalFg),
		swatch("Keyword", ui.Palette.Keyword),
	)...)
	body = append(body, "", header.Render("Resolution Roles"))
	body = append(body, rows(
		swatch("RoleOpen", ui.Palette.RoleOpen),
		swatch("RoleDone", ui.Palette.RoleDone),
		swatch("RoleClosed", ui.Palette.RoleClosed),
		swatch("RoleAttention", ui.Palette.RoleAttention),
		swatch("RoleInFlight", ui.Palette.RoleInFlight),
		swatch("RoleNeutral", ui.Palette.RoleNeutral),
	)...)
	body = append(body, "", header.Render("Chrome"))
	body = append(body, rows(
		swatch("Recessed", ui.Palette.Recessed),
		swatch("Border", ui.Palette.Border),
		swatch("Subtle", ui.Palette.Subtle),
		swatch("ButtonFg", ui.Palette.ButtonFg),
		swatch("LogoTop", ui.Palette.LogoTop),
		swatch("LogoBottom", ui.Palette.LogoBottom),
	)...)
	// Symbols are named rather than bare so the card doubles as the
	// lookup table for "which field renders which glyph" — the state
	// row is paired with the color each shape normally carries (see
	// state_grammar.go), the text row is uncolored punctuation.
	symbol := func(name, glyph string, col color.Color) string {
		return fmt.Sprintf("%s %-11s",
			lipgloss.NewStyle().Foreground(col).Render(glyph), name)
	}
	body = append(body, "", header.Render("Symbols — State"))
	body = append(body, rows(
		symbol("Check", ui.Palette.Check, ui.Palette.Success),
		symbol("Cross", ui.Palette.Cross, ui.Palette.Error),
		symbol("Active", ui.Palette.Active, ui.Palette.Primary),
		symbol("Attention", ui.Palette.Attention, ui.Palette.Warning),
		symbol("Waiting", ui.Palette.Waiting, ui.Palette.Info),
		symbol("Unknown", ui.Palette.Unknown, ui.Palette.Accent),
		symbol("Pending", ui.Palette.Pending, ui.Palette.Muted),
		symbol("Inactive", ui.Palette.Inactive, ui.Palette.Muted),
	)...)
	body = append(body, "", header.Render("Symbols — Text"))
	body = append(body, rows(
		symbol("Arrow", ui.Palette.Arrow, ui.Palette.NormalFg),
		symbol("Bullet", ui.Palette.Bullet, ui.Palette.NormalFg),
		symbol("Dot", ui.Palette.Dot, ui.Palette.NormalFg),
	)...)

	// Box-drawing chrome is fixed, not themeable (see ui/glyphs.go) —
	// shown here because the palette card is the glyph reference
	// surface, and a reader looking for ├ shouldn't conclude it's
	// missing.
	body = append(body, "", header.Render("Box Drawing — fixed, not themeable"))
	body = append(body, rows(
		symbol("Vertical", ui.BoxVertical, ui.Palette.Recessed),
		symbol("Horizontal", ui.BoxHorizontal, ui.Palette.Recessed),
		symbol("CornerTL", ui.BoxCornerTL, ui.Palette.Recessed),
		symbol("CornerTR", ui.BoxCornerTR, ui.Palette.Recessed),
		symbol("CornerBL", ui.BoxCornerBL, ui.Palette.Recessed),
		symbol("CornerBR", ui.BoxCornerBR, ui.Palette.Recessed),
		symbol("Tee", ui.BoxTee, ui.Palette.Recessed),
		symbol("Elbow", ui.BoxElbow, ui.Palette.Recessed),
	)...)

	ui.NewCard(ui.CardInfo, "palette").Raw(body...).Print()
}

func demoCards(cmd *cobra.Command) {
	// Static card — title, subtitle, and body with text, muted,
	// and key-value to show the primitive body types together.
	ui.NewCard(ui.CardInfo, "static card title").
		Subtitle("subtitle").
		Text("").
		Text("text body line").
		Text("").
		Muted("muted body line").
		Text("").
		KV(
			"Key", "value",
			"Another Key", "another value",
		).
		Print()

	// Data Card — structured state snapshot, no status glyph.
	// The heading uses an OSC 8 hyperlink so "EX-1234" is clickable
	// in supporting terminals (iTerm2, Terminal.app, Windows Terminal).
	issueURL := "https://jira.example.com/browse/EX-1234"
	ui.Details("issue \x1b]8;;"+issueURL+"\x1b\\EX-1234\x1b]8;;\x1b\\", ui.NewFields(
		"Title", "Add login page",
		"Status", "In Progress",
		"URL", issueURL,
	))

	// Heading with inline value — renders "Title: value" with the title
	// in the standard bold/primary heading style and the value muted, on
	// the same line.
	ui.NewCard(ui.CardSuccess, "preview").Value("feature/login-page").Print()

	// Card states — one bare card per state.
	ui.NewCard(ui.CardPending, "pending").Print()
	ui.NewCard(ui.CardSuccess, "success").Print()
	ui.NewCard(ui.CardSkipped, "skipped").Print()
	ui.NewCard(ui.CardFailed, "failed").Print()
	ui.NewCard(ui.CardInput, "input").Print()

	// Stdout and stderr — symmetric pair.
	ui.NewCard(ui.CardSuccess, "STDOUT").
		Stdout(
			"first line of captured output",
			"second line of captured output",
		).
		Print()

	ui.NewCard(ui.CardFailed, "STDERR").
		Stderr(
			"first line of error output",
			"second line of error output",
		).
		Print()
}

// demoWorkspaceMeta renders the workspace status meta block's
// Status (lifecycle stepper) + issue card pair in three variants:
// happy path, blocked (active dot becomes ✗ yellow), and degraded
// (tracker fetch failed — both cards collapse to their warning
// rows). Uses the real builders so the demo stays faithful to what
// `bosun status` and the lifecycle preambles render.
func demoWorkspaceMeta() {
	detail := issuepkg.Issue{
		Key:    "EX-1234",
		Title:  "Add login page",
		Type:   "Story",
		Status: "In Progress",
		URL:    "https://jira.example.com/browse/EX-1234",
	}
	buildWorkspaceStoryCard(detail, detail.Key, true).Print()

	blocked := detail
	blocked.Status = "Blocked"
	buildWorkspaceStoryCard(blocked, blocked.Key, true).Print()

	buildWorkspaceStoryCard(issuepkg.Issue{}, "EX-1234", false).Print()
}

func demoPlanCardStates() {
	// Static snapshot of the plan confirmation flow — pending header
	// card + confirm form with plan items as its content + buttons.
	// Routes through newPlanPendingHeader and snapshotForm so the
	// static rendering matches what runPlanCard produces when the
	// user actually runs a plan.
	plan := buildDemoPlan()
	var confirmed bool

	newPlanPendingHeader(plan).Print()

	snapshotForm(
		newConfirm().
			Title(plan.RenderItems()).
			Affirmative("Approve").
			Negative("Cancel").
			Value(&confirmed),
	)
}

// demoSummary renders one Reporter.Summary card with a mixed
// breakdown so the colored segments and the order-based glyph
// rollup are both visible at a glance.
func demoSummary() {
	ui.Default().Summary(
		"8 checks",
		[]ui.SummarySegment{
			{Count: 5, Label: "passed", Color: ui.Palette.Success},
			{Count: 2, Label: "warnings", Color: ui.Palette.Warning},
			{Count: 1, Label: "failed", Color: ui.Palette.Error},
		},
	)
}

func demoGroupsStatic() {
	r := ui.Default()

	r.Group("group: mixed outcomes with nesting", func(g ui.Reporter) {
		g.Complete("passed check")
		g.Skip("skipped: not configured")
		g.Group("inner group", func(inner ui.Reporter) {
			inner.Complete("inner child a")
			inner.Fail("inner child b: connection refused")
		})
		g.Complete("continued after failure")
	})
}

// demoItemCard renders a Card whose body uses the shared Card.Item
// primitive — colored per-row glyph + content. Placed between the
// group demo and the plan demo so the three card families can be
// compared side-by-side for layout alignment.
func demoItemCard() {
	success := lipgloss.NewStyle().Foreground(ui.Palette.Success).Render(ui.Palette.Check)
	info := lipgloss.NewStyle().Foreground(ui.Palette.Info).Render("↑")
	warning := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("↓")
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render(ui.Palette.Pending)

	ui.NewCard(ui.CardData, "card with items").
		Item(success, "first row · all good").
		Item(info, "second row · ahead 2").
		Item(warning, "third row · behind 1").
		Item(muted, "fourth row · pending").
		Print()
}

func demoTree() {
	ui.NewCard(ui.CardInfo, "tree").Tight().Print()
	ui.NewTree().Add(
		ui.Group("jira",
			ui.Leaf("◼︎", ui.Palette.Primary, "base_url", "https://jira.example.com"),
			ui.Leaf(ui.Palette.Attention, ui.Palette.Warning, "token", "••••••••"),
			ui.Leaf("◆", ui.Palette.Success, "project", "ABC"),
		),
		ui.Group("code_host",
			ui.Leaf("◼︎", ui.Palette.Primary, "provider", "github"),
			ui.Leaf("◻︎", ui.Palette.Muted, "auto_merge", "true"),
			ui.Leaf("◻︎", ui.Palette.Muted, "max_retries", "3"),
		),
		ui.Group("ui",
			ui.Leaf("◻︎", ui.Palette.Muted, "compact_header", "false"),
		),
	).Print()
}

func demoFormStatic() {
	// Static snapshot of a multi-field form. Routes through
	// snapshotForm so the prologue (leading spacer for multi-field)
	// and the theme/layout/help wiring all match what runForm
	// produces in interactive mode — no parallel construction code
	// to keep in sync.
	var (
		summary   string
		issueType string
		services  []string
		confirmed bool
	)

	ui.NewCard(ui.CardInput, "form").
		Subtitle("static snapshot (no interaction)").
		Tight().Print()

	snapshotForm(
		rawInput().
			Title("Summary").
			Placeholder("add user authentication flow").
			Value(&summary),
		huh.NewSelect[string]().
			Title("Type").
			Options(
				huh.NewOption("Story", "Story"),
				huh.NewOption("Bug", "Bug"),
				huh.NewOption("Task", "Task"),
			).
			Value(&issueType),
		// Multi-select rows share Card.Item's grid (glyph col 6,
		// content col 9) so the form lines up with the static card
		// that typically replaces it — compare with the Services
		// card shape in the preview flow.
		huh.NewMultiSelect[string]().
			Title("Services").
			Options(
				huh.NewOption("extracker · tag-api", "tag-api").Selected(true),
				huh.NewOption("extracker · pdfgen", "pdfgen"),
				huh.NewOption("host-ui", "host-ui").Selected(true),
			).
			Value(&services),
		newConfirm().
			Affirmative("Approve").
			Negative("Cancel").
			Value(&confirmed),
	)
}

// --- Interactive sections (gated by --interactive) ---

func demoSpinners() {
	// Fast operation — exercises the spinner timing floor. Without it,
	// BubbleTea v2 escape sequences leak into stdout when the program
	// quits before its terminal-mode queries round-trip.
	_ = ui.RunCard("spinner: fast (timing floor)", func() error {
		time.Sleep(5 * time.Millisecond)
		return nil
	})

	_ = ui.RunCard("spinner: success", func() error {
		time.Sleep(1500 * time.Millisecond)
		return nil
	})

	_ = ui.RunCard("spinner: failure", func() error {
		time.Sleep(1200 * time.Millisecond)
		return errors.New("permission denied")
	})
}

func demoGroups() {
	r := ui.Default()

	// Stagger delay between children so humans can track each step
	// appearing. 300ms is in the "noticeable but short" range per
	// UI animation research (100ms = instant, 200-400ms = short
	// transition, 1000ms = attention-holding).
	const step = 300 * time.Millisecond

	// Flat group, all succeed.
	r.Group("group: all succeed", func(g ui.Reporter) {
		time.Sleep(step)
		g.Complete("first child")
		time.Sleep(step)
		g.Complete("second child")
		time.Sleep(step)
		g.Complete("third child")
	})

	// Mixed outcomes — failure dominates aggregation.
	r.Group("group: mixed outcomes (failure dominates)", func(g ui.Reporter) {
		time.Sleep(step)
		g.Complete("succeeded")
		time.Sleep(step)
		g.Skip("skipped (precondition unmet)")
		time.Sleep(step)
		g.Fail("failed: permission denied")
		time.Sleep(step)
		g.Complete("ran anyway")
	})

	// All skipped — parent finalizes as skipped.
	r.Group("group: all skipped", func(g ui.Reporter) {
		time.Sleep(step)
		g.Skip("not configured")
		time.Sleep(step)
		g.Skip("not configured")
	})

	// Group with a Task child — exercises spinner indented under parent.
	r.Group("group: with spinner child", func(g ui.Reporter) {
		time.Sleep(step)
		g.Complete("pre-flight check")
		_ = g.Task("running async work", func() error {
			time.Sleep(800 * time.Millisecond)
			return nil
		})
		time.Sleep(step)
		g.Complete("post-flight check")
	})

	// Nested groups — child is itself a group.
	r.Group("group: nested", func(g ui.Reporter) {
		time.Sleep(step)
		g.Complete("outer first")
		g.Group("inner group", func(inner ui.Reporter) {
			time.Sleep(step)
			inner.Complete("inner child a")
			time.Sleep(step)
			inner.Complete("inner child b")
		})
		time.Sleep(step)
		g.Complete("outer last")
	})
}

func demoForms() error {
	var name string
	var confirmed bool

	// Single input with rewind.
	nameTitle := "form: single input"
	rewind := ui.NewCard(ui.CardInput, nameTitle).Tight().PrintRewindable()
	if err := runForm(
		rawInput().
			Description("Used as the worktree directory name").
			Placeholder("my-workspace").
			Value(&name),
	); err != nil {
		return err
	}
	rewind()
	ui.NewCard(ui.CardSuccess, nameTitle).
		Text(defaultStr(name, "(empty)")).
		Print()

	// Confirm with rewind.
	confirmTitle := "form: confirmation"
	rewind = ui.NewCard(ui.CardInput, confirmTitle).Tight().PrintRewindable()
	if err := runForm(
		newConfirm().
			Affirmative("Yes").
			Negative("No").
			Value(&confirmed),
	); err != nil {
		return err
	}
	rewind()
	ui.NewCard(ui.CardSuccess, confirmTitle).
		Text(boolStr(confirmed)).
		Print()

	// Multi-field form with rewind.
	var (
		issueSummary  string
		issueType     string
		issuePriority string
	)
	createTitle := "form: multi-field"
	rewind = ui.NewCard(ui.CardInput, createTitle).Tight().
		PrintRewindable()
	if err := runForm(
		rawInput().
			Title("Summary").
			Placeholder("Add user authentication flow").
			Value(&issueSummary),
		huh.NewSelect[string]().
			Title("Type").
			Options(
				huh.NewOption("Story", "Story"),
				huh.NewOption("Bug", "Bug"),
				huh.NewOption("Task", "Task"),
			).
			Value(&issueType),
		huh.NewSelect[string]().
			Title("Priority").
			Options(
				huh.NewOption("Low", "Low"),
				huh.NewOption("Medium", "Medium"),
				huh.NewOption("High", "High"),
			).
			Value(&issuePriority),
	); err != nil {
		return err
	}
	rewind()
	ui.NewCard(ui.CardSuccess, createTitle).
		KV(
			"Summary", defaultStr(issueSummary, "(empty)"),
			"Type", defaultStr(issueType, "(empty)"),
			"Priority", defaultStr(issuePriority, "(empty)"),
		).
		Print()

	return nil
}

func demoSlot() {
	slot := ui.NewSlot()

	_ = slot.Run("slot: run phase", func() error {
		time.Sleep(1 * time.Second)
		return nil
	})

	slot.Show(ui.NewCard(ui.CardInput, "slot: show phase").Tight())
	time.Sleep(500 * time.Millisecond)

	slot.Clear()

	ui.NewCard(ui.CardSuccess, "slot: finalized").
		Text("api").
		Print()
}

func demoPlanApply(cmd *cobra.Command) {
	// Full lifecycle: proposed → confirmation gate → apply → success.
	plan := buildDemoPlan()
	actions := []PlanAction{
		{Run: func() error { time.Sleep(400 * time.Millisecond); return nil }},
		{Run: func() error { time.Sleep(300 * time.Millisecond); return nil }},
		{Run: func() error { time.Sleep(500 * time.Millisecond); return nil }},
		{Run: func() error { time.Sleep(200 * time.Millisecond); return nil }},
	}
	_ = runPlanCard(cmd, plan, actions, PlanOpts{Confirm: true, Apply: true})
}

// demoPlanGated shows the apply gate: one deploy lands, the other
// fails, and the status transition queued behind them is withheld
// rather than applied. The card finalizes Partial — "1 failed, 1
// skipped, 1 applied" — over a muted "(skipped)" row, the visual
// vocabulary for work that was planned and deliberately not done,
// distinct from both the ✗ of a failure and the = of no change.
//
// Two deploys rather than one so all three counts appear at once; with
// a single failing deploy nothing succeeds and the card finalizes
// Failure instead.
func demoPlanGated(cmd *cobra.Command) {
	ui.Info("apply gate (a queued action withheld behind a failure)")

	statusSkip := &ui.SkipRef{}
	plan := ui.NewPlan().
		Add(ui.PlanCreate, "deploy", "service", "web", "v1.2.4").
		Add(ui.PlanCreate, "deploy", "service", "api", "v1.2.4").
		AddWithRefs(ui.PlanModify, "status", "issue", "ABC-123",
			"In Progress → Done", nil, statusSkip)

	actions := []PlanAction{
		{Run: func() error { time.Sleep(300 * time.Millisecond); return nil }},
		{Run: func() error {
			time.Sleep(400 * time.Millisecond)
			return fmt.Errorf("workflow dispatch 422: no ref v1.2.4")
		}},
		{
			Run:                  func() error { return nil },
			RequiresPriorSuccess: true,
			Skip:                 statusSkip,
		},
	}
	_ = runPlanCard(cmd, plan, actions, PlanOpts{Confirm: false, Apply: true})
}

func demoPlanDryRun(cmd *cobra.Command) {
	// Dry-run: plan renders in proposed state without applying.
	ui.Info("dry-run mode (apply disabled)")
	plan := buildDemoPlan()
	_ = runPlanCard(cmd, plan, nil, PlanOpts{Confirm: false, Apply: false})
}

func demoPlanNoWork(cmd *cobra.Command) {
	// No-work: all items are no-change, plan finalizes as verified
	// (PlanVerified — the sibling of PlanSuccess for "we checked,
	// nothing needed doing").
	ui.Info("no-work branch (all items unchanged)")
	plan := ui.NewPlan().
		Add(ui.PlanNoChange, "branch", "repo", "api", "feature/ABC-123").
		Add(ui.PlanNoChange, "branch", "repo", "web", "feature/ABC-123")
	_ = runPlanCard(cmd, plan, nil, PlanOpts{Confirm: true, Apply: true})
}

// --- Helpers ---

// demoContinue shows a gate card between demo sections via the
// Dialog component. The title names the upcoming batch of
// components; the body invites review or starts the run. After
// the user chooses Continue, the dialog is gone and a section
// heading card replaces it for the output that follows.
func demoContinue(title string, first bool) error {
	body := "Review the output above, then continue when ready."
	if first {
		body = "Each section pauses so you can review at your own pace."
	}

	confirmed, err := NewDialog(title).
		Description(body).
		Affirmative("Continue").
		Negative("Stop").
		Show()
	if err != nil {
		return err
	}
	if !confirmed {
		return ErrCancelled
	}
	// Re-print the title as a section heading for the output that follows.
	ui.NewCard(ui.CardInfo, title).Print()
	return nil
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func boolStr(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// demoSummaryInteractive shows the summary card by way of a form
// that gathers counts from the user, then re-renders the summary
// card with those values. Lets a designer play with the order-based
// glyph rollup and the per-segment color treatment without leaving
// the demo.
func demoSummaryInteractive() error {
	passedStr := "5"
	warnedStr := "2"
	failedStr := "1"

	title := "summary: counts"
	rewind := ui.NewCard(ui.CardInput, title).Tight().PrintRewindable()
	if err := runForm(
		rawInput().Title("Passed").Value(&passedStr),
		rawInput().Title("Warnings").Value(&warnedStr),
		rawInput().Title("Failed").Value(&failedStr),
	); err != nil {
		return err
	}
	rewind()

	passed, _ := strconv.Atoi(passedStr)
	warned, _ := strconv.Atoi(warnedStr)
	failed, _ := strconv.Atoi(failedStr)
	total := passed + warned + failed

	ui.Default().Summary(
		fmt.Sprintf("%d %s", total, pluralize(total, "check", "checks")),
		[]ui.SummarySegment{
			{Count: passed, Label: "passed", Color: ui.Palette.Success},
			{Count: warned, Label: pluralize(warned, "warning", "warnings"), Color: ui.Palette.Warning},
			{Count: failed, Label: "failed", Color: ui.Palette.Error},
		},
	)
	return nil
}
