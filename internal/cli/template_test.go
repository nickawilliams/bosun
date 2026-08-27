package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// captureReporter installs a CaptureReporter as the default for the
// test and returns it, so ui.Warning calls are assertable.
func captureReporter(t *testing.T) *ui.CaptureReporter {
	t.Helper()
	prev := ui.Default()
	rep := ui.NewCaptureReporter()
	ui.SetDefault(rep)
	t.Cleanup(func() { ui.SetDefault(prev) })
	return rep
}

// warnings returns every label ui.Warning was called with.
func warnings(rep *ui.CaptureReporter) []string {
	var out []string
	for _, ev := range rep.OfKind(ui.CaptureWarning) {
		out = append(out, ev.Label)
	}
	return out
}

// TestTemplateVocabularyIsShared is the property this whole change
// exists to establish: one spelling per value, across every context a
// user can write a template for. Asserting it as a table rather than
// per-context is deliberate — a future context that reintroduces its
// own name for the issue key has to come past this test.
func TestTemplateVocabularyIsShared(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	detail := issue.Issue{Title: "Add widget", Type: "Story", URL: "https://tracker.test/PROJ-1"}
	ref := issueRefFrom("PROJ-1", detail)

	// The issue key renders from the same expression in all three
	// contexts that carry one.
	t.Run("issue key", func(t *testing.T) {
		viper.Set("vcs.branch.template", "{{.Issue.Key}}")
		got, err := buildBranchName(issue.Issue{Key: "PROJ-1", Type: "Story", Title: "Add widget"}, "")
		if err != nil || got != "PROJ-1" {
			t.Errorf("branch = (%q, %v), want PROJ-1", got, err)
		}

		viper.Set("code_host.pr.title_template", "{{.Issue.Key}}")
		if got := buildPRTitle(prTemplateData{Issue: ref}); got != "PROJ-1" {
			t.Errorf("pr title = %q, want PROJ-1", got)
		}

		viper.Set("notification.templates.review", "{{.Issue.Key}}")
		if got := buildNotifyContent("review", notifyTemplateData{Issue: ref}).Text; got != "PROJ-1" {
			t.Errorf("notification = %q, want PROJ-1", got)
		}
	})

	// The preview name renders from the same expression in both
	// contexts that carry one — the split that motivated the change.
	t.Run("preview name", func(t *testing.T) {
		viper.Set("preview.url_template", "https://{{.Preview.Name}}.test")
		if got := renderStageURL("preview", "brave-falcon"); got != "https://brave-falcon.test" {
			t.Errorf("stage URL = %q, want the rendered name", got)
		}

		viper.Set("notification.templates.review", "{{.Preview.Name}}")
		content := buildNotifyContent("review", notifyTemplateData{
			Issue:   ref,
			Preview: preview.Ref{Name: "brave-falcon"},
		})
		if content.Text != "brave-falcon" {
			t.Errorf("notification = %q, want brave-falcon", content.Text)
		}
	})
}

// TestIssueRefFromCarriesEveryField guards the helper both review
// notification sites share. A field it drops is a field that silently
// stops being available to templates, and — because the assess-time
// hash and the apply-time send both read it — one that would report
// the notification as unchanged when it changed.
func TestIssueRefFromCarriesEveryField(t *testing.T) {
	got := issueRefFrom("PROJ-1", issue.Issue{
		Title:       "Add widget",
		Type:        "Story",
		URL:         "https://tracker.test/PROJ-1",
		Description: "body text",
		TypeIconURL: "https://tracker.test/icon.png",
	})
	want := issueRef{
		Key:         "PROJ-1",
		Title:       "Add widget",
		Type:        "Story",
		URL:         "https://tracker.test/PROJ-1",
		Description: "body text",
		IconURL:     "https://tracker.test/icon.png",
	}
	if got != want {
		t.Errorf("issueRefFrom() = %+v, want %+v", got, want)
	}
	// Slug is deliberately absent: it is derived per branch template
	// from a possibly user-supplied override, not carried on the issue.
	if got.Slug != "" {
		t.Errorf("Slug = %q, want empty — buildBranchName owns it", got.Slug)
	}
}

// TestNotifyContentCarriesEveryIssueField guards the boundary
// translation buildNotifyContent performs, which is the one place the
// template vocabulary stops being a single struct: cli.issueRef is
// copied field-by-field into notify.IssueRef, because the two are
// different concerns that happen to overlap — the notification model
// has no use for Slug, and the template vocabulary should not inherit
// whatever the notification model grows next.
//
// The copy is right; being silent when it falls behind is not. A field
// added to notify.IssueRef and missed here reaches the Slack adapter as
// a zero value with nothing to show for it — the same failure
// issueRefFrom exists to prevent, one layer down. Reflection rather
// than a literal comparison so the test fails when either type grows.
func TestNotifyContentCarriesEveryIssueField(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	// Every field distinct and non-zero, so a field the copy drops
	// shows up as the zero value rather than coincidentally matching.
	content := buildNotifyContent("review", notifyTemplateData{
		Issue: issueRef{
			Key:         "PROJ-1",
			Title:       "Add widget",
			Slug:        "add-widget",
			Type:        "Story",
			URL:         "https://tracker.test/PROJ-1",
			Description: "body text",
			IconURL:     "https://tracker.test/icon.png",
		},
	})
	if content.Issue == nil {
		t.Fatal("Issue = nil, want populated issue data")
	}

	// Slug is the deliberate exception: it is a branch-naming concern
	// and the notification model has no field for it. If notify ever
	// grows one, this list is what has to change with it.
	got := reflect.ValueOf(*content.Issue)
	for i := range got.NumField() {
		field := got.Type().Field(i)
		if got.Field(i).IsZero() {
			t.Errorf("notify.IssueRef.%s was not carried across from issueRef", field.Name)
		}
	}

	// The other direction: a field on issueRef with no counterpart in
	// notify.IssueRef is fine, but the set of exceptions is not allowed
	// to grow quietly.
	exceptions := map[string]bool{"Slug": true}
	notifyFields := map[string]bool{}
	for i := range got.NumField() {
		notifyFields[got.Type().Field(i).Name] = true
	}
	src := reflect.TypeOf(issueRef{})
	for i := range src.NumField() {
		name := src.Field(i).Name
		if !notifyFields[name] && !exceptions[name] {
			t.Errorf("issueRef.%s reaches no notification field and is not a declared exception", name)
		}
	}
}

func TestTemplateMigrationHint(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		extra   map[string]string
		want    string
	}{
		{
			name:    "clean pattern yields nothing",
			pattern: "{{.Issue.Key}} {{.Issue.Title}}",
		},
		{
			// The renamed-not-just-moved case: IssueNumber never held
			// a number, and the hint has to name Key rather than echo
			// the old word back.
			name:    "IssueNumber points at Issue.Key",
			pattern: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}",
			want:    "{{.IssueNumber}} is now {{.Issue.Key}}; {{.IssueSlug}} is now {{.Issue.Slug}}",
		},
		{
			name:    "preview fields",
			pattern: "{{.PreviewName}} at {{.PreviewURL}}",
			want:    "{{.PreviewName}} is now {{.Preview.Name}}; {{.PreviewURL}} is now {{.Preview.URL}}",
		},
		{
			// IssueIconURL and IconURL differ by a prefix, and only
			// the first was retired. Reporting the survivor would send
			// the reader to change a field that still works.
			name:    "the surviving IconURL is not reported",
			pattern: "{{.IconURL}}",
		},
		{
			name:    "extra carries a context-specific retirement",
			pattern: "https://{{.Name}}.test",
			extra:   map[string]string{"Name": "Preview.Name"},
			want:    "{{.Name}} is now {{.Preview.Name}}",
		},
		{
			// Bare Name is only retired where extra says so. A range
			// variable's .Name in a notification template is live.
			name:    "bare Name is not reported without extra",
			pattern: "{{range .Items}}{{.Name}}{{end}}",
		},
		{
			// Each retired field is named once however often it
			// appears, and the order is stable — map iteration is not.
			name:    "repeats collapse",
			pattern: "{{.IssueKey}} {{.IssueKey}}",
			want:    "{{.IssueKey}} is now {{.Issue.Key}}",
		},
		{
			// The worst false positive: a CORRECT template failing for
			// an unrelated reason must not be told to fix the one thing
			// it got right. A substring scan for ".Name" matches
			// ".Preview.Name", so the advice would point away from the
			// real fault (.Typo) at the thing already migrated.
			name:    "the new spelling is not mistaken for the old one",
			pattern: "https://{{.Preview.Name}}-{{.Typo}}.test",
			extra:   map[string]string{"Name": "Preview.Name"},
		},
		{
			// A longer identifier that merely starts with a retired
			// name is a different field, and this project never
			// retired it.
			name:    "a longer identifier is not a retired one",
			pattern: "{{.IssueKeyword}}",
		},
		{
			// Whitespace inside the action is legal Go template syntax
			// and must not hide a genuinely stale field.
			name:    "spaces inside the action still match",
			pattern: "{{ .IssueKey }}",
			want:    "{{.IssueKey}} is now {{.Issue.Key}}",
		},
		{
			// extra wins over the shared map for the same key, rather
			// than the answer depending on which loop ran first.
			name:    "extra overrides the shared map",
			pattern: "{{.PreviewName}}",
			extra:   map[string]string{"PreviewName": "Somewhere.Else"},
			want:    "{{.PreviewName}} is now {{.Somewhere.Else}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateMigrationHint(tt.pattern, tt.extra); got != tt.want {
				t.Errorf("templateMigrationHint() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTemplateMigrationHintIsStable pins the ordering. The hint is
// built by ranging two maps, and Go randomizes that — an unsorted
// result would produce a different sentence on different runs for the
// same config.
func TestTemplateMigrationHintIsStable(t *testing.T) {
	pattern := "{{.IssueKey}} {{.IssueTitle}} {{.IssueURL}} {{.PreviewName}}"
	first := templateMigrationHint(pattern, map[string]string{"Name": "Preview.Name"})
	for range 20 {
		if got := templateMigrationHint(pattern, map[string]string{"Name": "Preview.Name"}); got != first {
			t.Fatalf("hint varies between calls:\n %q\n %q", first, got)
		}
	}
}

// TestReportTemplateFailureNamesKeyAndMigration pins what the user
// actually reads: the config key that carries the broken template, the
// underlying error, and the replacement for the retired variable.
func TestReportTemplateFailureNamesKeyAndMigration(t *testing.T) {
	rep := captureReporter(t)
	reportTemplateFailure(
		"vcs.branch.template",
		"{{.IssueNumber}}",
		errors.New("can't evaluate field IssueNumber"),
		nil,
	)

	got := warnings(rep)
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want exactly one", got)
	}
	for _, want := range []string{"vcs.branch.template", "can't evaluate field IssueNumber", "{{.Issue.Key}}"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("warning %q does not carry %q", got[0], want)
		}
	}
}

// TestLegacyTemplateIsReportedNotSilent is the reason this change is a
// warned migration rather than a silent break. Each of these paths
// falls back on a template error — a hardcoded title, an empty body, no
// URL — so without the report a config still on the old vocabulary
// would look like it was being honored.
func TestLegacyTemplateIsReportedNotSilent(t *testing.T) {
	tests := []struct {
		name      string
		configKey string
		pattern   string
		render    func() string
		wantValue string // "" means the path degrades to empty
	}{
		{
			name:      "pr title falls back but says so",
			configKey: "code_host.pr.title_template",
			pattern:   "{{.IssueType}} {{.IssueKey}}",
			render: func() string {
				return buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
			},
			wantValue: "[PROJ-1] Add widget",
		},
		{
			name:      "pr body degrades but says so",
			configKey: "code_host.pr.body_template",
			pattern:   "{{.IssueURL}}",
			render: func() string {
				return buildPRBody(prTemplateData{Issue: issueRef{Key: "PROJ-1"}})
			},
		},
		{
			name:      "notification degrades but says so",
			configKey: "notification.templates.review",
			pattern:   "{{.PreviewName}}",
			render: func() string {
				return buildNotifyContent("review", notifyTemplateData{
					Issue:   issueRef{Key: "PROJ-1"},
					Preview: preview.Ref{Name: "brave-falcon"},
				}).Text
			},
		},
		{
			name:      "stage URL degrades but says so",
			configKey: "preview.url_template",
			pattern:   "https://{{.Name}}.test",
			render:    func() string { return renderStageURL("preview", "brave-falcon") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tt.configKey, tt.pattern)
			rep := captureReporter(t)

			if got := tt.render(); got != tt.wantValue {
				t.Errorf("render = %q, want %q", got, tt.wantValue)
			}

			got := warnings(rep)
			if len(got) == 0 {
				t.Fatalf("a stale %s was honored silently", tt.configKey)
			}
			if !strings.Contains(got[0], tt.configKey) {
				t.Errorf("warning %q does not name the config key", got[0])
			}
			if !strings.Contains(got[0], " is now ") {
				t.Errorf("warning %q does not name a replacement", got[0])
			}
		})
	}
}

// TestBuildPRTitleDefaultIsSilent guards the other half: the built-in
// default is not user config, so a run with no title_template set must
// not warn about one.
func TestBuildPRTitleDefaultIsSilent(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	rep := captureReporter(t)

	got := buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
	if got != "[PROJ-1] Add widget" {
		t.Errorf("title = %q, want the built-in default", got)
	}
	if w := warnings(rep); len(w) != 0 {
		t.Errorf("the built-in default warned: %v", w)
	}
}

// TestMalformedTemplateIsReported covers the parse arm, which is a
// different failure from the execute arm above: a pattern that does not
// parse has no action list at all. templateMigrationHint scans the raw
// string rather than walking parsed actions precisely so it still has
// something to say here — a stale variable inside an unclosed action is
// the likeliest way both faults arrive together.
func TestMalformedTemplateIsReported(t *testing.T) {
	// Unclosed action: fails to parse, and still names a retired field.
	const malformed = "{{.IssueKey"

	tests := []struct {
		name      string
		configKey string
		render    func() string
		wantValue string
	}{
		{
			name:      "pr title",
			configKey: "code_host.pr.title_template",
			render: func() string {
				return buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
			},
			wantValue: "[PROJ-1] Add widget",
		},
		{
			name:      "pr body",
			configKey: "code_host.pr.body_template",
			render:    func() string { return buildPRBody(prTemplateData{Issue: issueRef{Key: "PROJ-1"}}) },
		},
		{
			name:      "notification",
			configKey: "notification.templates.review",
			render: func() string {
				return buildNotifyContent("review", notifyTemplateData{Issue: issueRef{Key: "PROJ-1"}}).Text
			},
		},
		{
			name:      "stage URL",
			configKey: "preview.url_template",
			render:    func() string { return renderStageURL("preview", "brave-falcon") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tt.configKey, malformed)
			rep := captureReporter(t)

			if got := tt.render(); got != tt.wantValue {
				t.Errorf("render = %q, want %q", got, tt.wantValue)
			}
			got := warnings(rep)
			if len(got) == 0 {
				t.Fatalf("a malformed %s failed silently", tt.configKey)
			}
			if !strings.Contains(got[0], tt.configKey) {
				t.Errorf("warning %q does not name the config key", got[0])
			}
			// The migration advice survives an unparseable pattern.
			if !strings.Contains(got[0], "{{.Issue.Key}}") {
				t.Errorf("warning %q lost the migration hint on a parse failure", got[0])
			}
		})
	}
}

// TestBuildBranchNameMalformedTemplate is the branch path's parse arm.
// It returns the parse error bare — there is no rendered name to fall
// back to, and the error text already quotes the offending pattern.
func TestBuildBranchNameMalformedTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Issue.Key")

	got, err := buildBranchName(issue.Issue{Key: "PROJ-1", Type: "Story", Title: "Add widget"}, "")
	if err == nil {
		t.Fatalf("buildBranchName() = %q, want a parse error", got)
	}
}

// TestBuildBranchNameNonLegacyFailureHasNoHint guards against advice
// that misleads: a template naming a field that never existed is a
// typo, not a migration, and telling the user to rename something they
// never wrote would send them looking in the wrong place.
func TestBuildBranchNameNonLegacyFailureHasNoHint(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Category}}/{{.Nonsense}}")

	_, err := buildBranchName(issue.Issue{Key: "PROJ-1", Type: "Story", Title: "Add widget"}, "")
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
	if strings.Contains(err.Error(), " is now ") {
		t.Errorf("err %q offers a migration for a field that was never retired", err)
	}
}

// TestStageURLTemplateRejectsLegacyAtConstruction is the fix for the
// hole the diagnostic originally left. Both preview adapters render
// URLTemplate themselves, deep inside Get/Inspect/List, and return ""
// on error — a legacy {{.Name}} PARSES, so it survived construction and
// then produced blank URLs everywhere in silence. Proving it can render
// is the only check positioned before anything depends on it.
func TestStageURLTemplateRejectsLegacyAtConstruction(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("preview.url_template", "https://{{.Name}}.test")

	tmpl, err := stageURLTemplate("preview")
	if err == nil {
		t.Fatal("a legacy url_template was admitted; every preview URL would render empty")
	}
	if tmpl != nil {
		t.Error("a rejected template was still returned")
	}
	for _, want := range []string{"preview.url_template", "{{.Preview.Name}}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not carry %q", err, want)
		}
	}
}

func TestStageURLTemplateAcceptsCurrentVocabulary(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("preview.url_template", "https://{{.Preview.Name}}.test")

	tmpl, err := stageURLTemplate("preview")
	if err != nil {
		t.Fatalf("stageURLTemplate: %v", err)
	}
	if tmpl == nil {
		t.Fatal("a valid template was dropped")
	}
}

func TestStageURLTemplateUnsetIsNotAnError(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	tmpl, err := stageURLTemplate("preview")
	if err != nil || tmpl != nil {
		t.Errorf("stageURLTemplate() = (%v, %v), want (nil, nil) when unset", tmpl, err)
	}
}

func TestStageURLTemplateRejectsUnparseable(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("preview.url_template", "https://{{.Preview.Name")

	if _, err := stageURLTemplate("preview"); err == nil {
		t.Fatal("an unparseable url_template was admitted")
	}
}

// TestTemplateWarningIsReportedOnce covers the dedup. The render paths
// are called repeatedly by design — buildPRTitle once for the shared
// prompt and again per repository — so one stale template would
// otherwise print the same sentence once per call, some of it from
// inside a plan's assessment spinner.
func TestTemplateWarningIsReportedOnce(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	resetTemplateReports()
	t.Cleanup(resetTemplateReports)
	viper.Set("code_host.pr.title_template", "{{.IssueKey}}")
	rep := captureReporter(t)

	data := prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}}
	for range 5 {
		buildPRTitle(data)
	}

	if got := warnings(rep); len(got) != 1 {
		t.Errorf("warnings = %v, want exactly one for five identical failures", got)
	}
}

// TestTemplateWarningResetsPerRun is the other half: the dedup is
// per command, so a second run reports again rather than inheriting
// the first run's silence.
func TestTemplateWarningResetsPerRun(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	resetTemplateReports()
	t.Cleanup(resetTemplateReports)
	viper.Set("code_host.pr.title_template", "{{.IssueKey}}")
	rep := captureReporter(t)

	data := prTemplateData{Issue: issueRef{Key: "PROJ-1"}}
	buildPRTitle(data)
	resetTemplateReports()
	buildPRTitle(data)

	if got := warnings(rep); len(got) != 2 {
		t.Errorf("warnings = %v, want one per run", got)
	}
}

// TestBuiltInTemplateFailureBlamesNobody pins that a bosun built-in is
// never reported against a config key the user did not set. The
// built-ins cannot currently fail, so this exercises the guard
// directly rather than through a contrived failure.
func TestBuiltInTemplateFailureBlamesNobody(t *testing.T) {
	resetTemplateReports()
	t.Cleanup(resetTemplateReports)
	rep := captureReporter(t)

	reportTemplateFailure("", "{{.IssueKey}}", errors.New("boom"), nil)

	if got := warnings(rep); len(got) != 0 {
		t.Errorf("a built-in failure was blamed on config: %v", got)
	}
}

// TestBranchTemplateExposesTheWholeIssue guards the regression the
// namespacing introduced and this change closes: a field the context
// advertises but nobody populates renders as "" rather than failing,
// so {{.Issue.Type}} in a branch template silently produced a
// leading-slash refname. Every advertised field is populated now.
func TestBranchTemplateExposesTheWholeIssue(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Issue.Type}}/{{.Issue.Key}}/{{.Issue.Title}}/{{.Issue.URL}}")

	got, err := buildBranchName(issue.Issue{
		Key:   "PROJ-1",
		Type:  "Story",
		Title: "Add widget",
		URL:   "https://tracker.test/PROJ-1",
	}, "")
	if err != nil {
		t.Fatalf("buildBranchName: %v", err)
	}
	want := "Story/PROJ-1/Add widget/https://tracker.test/PROJ-1"
	if got != want {
		t.Errorf("branch = %q, want %q — an unpopulated field renders empty, not as an error", got, want)
	}
}

// TestBuildBranchNameAbortsOnStaleTemplate is the one path that refuses
// rather than degrading: a branch name is the run's identity, and half
// a name is worse than a refusal. The migration hint still has to reach
// the user, which for this path means riding on the error.
func TestBuildBranchNameAbortsOnStaleTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Category}}/{{.IssueNumber}}")

	got, err := buildBranchName(issue.Issue{Key: "PROJ-1", Type: "Story", Title: "Add widget"}, "")
	if err == nil {
		t.Fatalf("buildBranchName() = %q, want an error on a stale template", got)
	}
	for _, want := range []string{"vcs.branch.template", "{{.Issue.Key}}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not carry %q", err, want)
		}
	}
}
