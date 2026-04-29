package cli

import (
	"errors"
	"fmt"
	"time"

	"charm.land/huh/v2"
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
			demoCards(cmd)
			demoTree()
			ui.ClearBreak()
			fmt.Println()
			demoPlanCardStates()
			demoFormStatic()

			if interactive {
				demoSpinners()
				if err := demoForms(); err != nil {
					return err
				}
				demoSlot()
				demoPlanApply()
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

func demoCards(cmd *cobra.Command) {
	// Root card — breadcrumb title, subtitle, and body.
	rootCard(cmd, "comprehensive UI component reference").
		Text("Lorem ipsum dolor sit amet, consectetur adipisicing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.").
		Print()

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

func demoPlanCardStates() {
	// Static snapshot of the plan confirmation flow — plan card
	// title + confirm with plan items as its content + buttons.
	plan := buildDemoPlan()
	var confirmed bool

	ui.NewCard(ui.CardInput, "pending: "+plan.Summary()).Tight().Print()

	f := huh.NewForm(huh.NewGroup(
		newConfirm().
			Title(plan.RenderItems()).
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmed),
	)).
		WithTheme(formTheme).
		WithLayout(ui.NewTimelineLayout()).
		WithShowHelp(true)

	f.Init()
	fmt.Print(f.View())
	fmt.Print("\n\n")
}

func demoTree() {
	ui.NewCard(ui.CardInfo, "tree").Tight().Print()
	ui.NewTree().Add(
		ui.Group("jira",
			ui.Leaf("◼︎", ui.Palette.Primary, "base_url", "https://jira.example.com"),
			ui.Leaf("▲", ui.Palette.Warning, "token", "••••••••"),
			ui.Leaf("◆", ui.Palette.Success, "project", "ABC"),
		),
		ui.Group("github",
			ui.Leaf("◼︎", ui.Palette.Primary, "owner", "acme-corp"),
			ui.Leaf("◻︎", ui.Palette.Muted, "auto_merge", "true"),
			ui.Leaf("◻︎", ui.Palette.Muted, "max_retries", "3"),
		),
		ui.Leaf("◻︎", ui.Palette.Muted, "display_mode", "comfy"),
	).Print()
}

func demoFormStatic() {
	// Static snapshot of a multi-field form — Init() + View()
	// renders the focused state without running the interactive loop.
	var (
		summary   string
		issueType string
		confirmed bool
	)

	ui.NewCard(ui.CardInput, "form").
		Subtitle("static snapshot (no interaction)").
		Tight().Print()

	f := huh.NewForm(huh.NewGroup(
		huh.NewInput().
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
		newConfirm().
			Affirmative("Apply").
			Negative("Cancel").
			Value(&confirmed),
	)).
		WithTheme(formTheme).
		WithLayout(ui.NewTimelineLayout()).
		WithShowHelp(true)

	f.Init()
	fmt.Print(f.View())
	fmt.Print("\n\n")
}

// --- Interactive sections (gated by --interactive) ---

func demoSpinners() {
	_ = ui.RunCard("spinner: success", func() error {
		time.Sleep(1500 * time.Millisecond)
		return nil
	})

	_ = ui.RunCard("spinner: failure", func() error {
		time.Sleep(1200 * time.Millisecond)
		return errors.New("permission denied")
	})
}

func demoForms() error {
	var name string
	var confirmed bool

	// Single input with rewind.
	nameTitle := "form: single input"
	rewind := ui.NewCard(ui.CardInput, nameTitle).Tight().PrintRewindable()
	if err := runForm(
		huh.NewInput().
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
	rewind = ui.NewCard(ui.CardInput, createTitle).
		PrintRewindable()
	if err := runForm(
		huh.NewInput().
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

func demoPlanApply() {
	plan := buildDemoPlan()
	pc := ui.NewPlanCard(plan)

	_ = pc.RunApply([]func() error{
		func() error { time.Sleep(400 * time.Millisecond); return nil },
		func() error { time.Sleep(300 * time.Millisecond); return nil },
		func() error { time.Sleep(500 * time.Millisecond); return nil },
		func() error { time.Sleep(200 * time.Millisecond); return nil },
	})
}

// --- Helpers ---

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
